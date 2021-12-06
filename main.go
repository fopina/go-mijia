package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/examples/lib/dev"
	log "github.com/sirupsen/logrus"
)

var (
	scan    = flag.Bool("scan", false, "Scan BLE peripherals - using this flag ignores every flag except -sd")
	device  = flag.String("device", "default", "implementation of ble")
	name    = flag.String("name", "ATC", "name of remote peripheral")
	addr    = flag.String("addr", "", "address of remote peripheral (MAC on Linux, UUID on OS X)")
	atc     = flag.Bool("atc", false, "ATC mode: do not connect, simply monitor ATC custom advertisement data - this only uses -sub parameter, not -sd")
	sub     = flag.Duration("sub", 0, "subscribe to notification and indication (or monitor, in case of -atc) for a specified period, 0 for indefinitely")
	sd      = flag.Duration("sd", 15*time.Second, "scanning duration, 0 for indefinitely")
	quiet   = flag.Bool("quiet", false, "Do not show notifications in stdout")
	debug   = flag.Bool("debug", false, "Debug verbosity")
	web     = flag.Bool("web", false, "Make data available via HTTP (ignores -sub)")
	webBind = flag.String("web-bind", "127.0.0.1:8989", "Address and port to bind the web webserver (-web)")

	advertisementMode      = false
	isConnected            = false
	temperature            = 0.0
	humidity          byte = 0
	battery           byte = 0
	lastUpdate        time.Time
	// used by ATC mode
	lastFrame byte = 0
)

func main() {
	flag.Parse()

	if *debug {
		log.SetLevel(log.DebugLevel)
	}

	d, err := dev.NewDevice(*device)
	if err != nil {
		log.Fatalf("can't new device : %s", err)
	}
	ble.SetDefaultDevice(d)

	if *scan {
		startScan()
		return
	}

	// Default to search device with name of ATC (or specified by user).
	filter := func(a ble.Advertisement) bool {
		return strings.ToUpper(a.LocalName()) == strings.ToUpper(*name)
	}

	// If addr is specified, search for addr instead.
	if len(*addr) != 0 {
		filter = func(a ble.Advertisement) bool {
			return strings.ToUpper(a.Addr().String()) == strings.ToUpper(*addr)
		}
	}

	var cln ble.Client
	var done <-chan struct{}

	if *atc {
		advertisementMode = true
		done = atcMode(filter)
	} else {
		cln, done = connectMode(filter)
	}

	if *web {
		startWeb()
	} else {
		if *sub == 0 {
			for {
				time.Sleep(time.Hour)
			}
		} else {
			time.Sleep(*sub)
		}
	}

	if !*atc {
		// Disconnect the connection. (On OS X, this might take a while.)
		log.Info("Disconnecting [ %s ]... (this might take up to few seconds on OS X)\n", cln.Addr())
		cln.CancelConnection()
	}
	<-done
}

func connectMode(filter ble.AdvFilter) (ble.Client, <-chan struct{}) {
	// Scan for specified durantion, or until interrupted by user.
	log.Info("Scanning for %s...\n", *sd)
	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), *sd))
	cln, err := ble.Connect(ctx, filter)
	if err != nil {
		log.Fatalf("can't connect : %s", err)
	}
	isConnected = true

	// Make sure we had the chance to print out the message.
	done := make(chan struct{})
	// Normally, the connection is disconnected by us after our exploration.
	// However, it can be asynchronously disconnected by the remote peripheral.
	// So we wait(detect) the disconnection in the go routine.
	go func() {
		<-cln.Disconnected()
		log.Infof("[ %s ] is disconnected", cln.Addr())
		// should app be terminated here? or restart scanning?
		isConnected = false
		close(done)
	}()

	/*
			Characteristic: ebe0ccc17a0a4b0c8a1a6ff2997da3a6 , Property: 0x12 (NR), Handle(0x35), VHandle(0x36)
		        Descriptor: 2901 Characteristic User Description, Handle(0x37)
		        Value         54656d706572617475726520616e642048756d696469 | "Temperature and Humidi"
		        Descriptor: 2902 Client Characteristic Configuration, Handle(0x38)
		        Value         0000 | "\x00\x00"
	*/

	log.Info("Discovering profile...")
	p, err := cln.DiscoverProfile(true)
	if err != nil {
		log.Fatalf("can't discover profile: %s", err)
	}

	// find characteritic by descriptor
	tempChar := findTemperatureCharacteristic(cln, p)

	if tempChar == nil {
		log.Fatalf("can't find right Temperature and Humidity characteristic in this peripheral")
	}

	// subscribe notifications
	subscribe(cln, tempChar)

	defer func() {
		if err := cln.Unsubscribe(tempChar, false); err != nil {
			log.Fatalf("unsubscribe failed: %s", err)
		}
		log.Info("-- Unsubscribe to notification --")
	}()

	return cln, done
}

func atcMode(filter ble.AdvFilter) <-chan struct{} {
	handleAdv := func(a ble.Advertisement) {
		for _, l := range a.ServiceData() {
			log.Debug("got something")
			if l.UUID.Equal(ble.UUID16(0x181a)) {
				buf := bytes.NewReader(l.Data)
				buf.Seek(6, io.SeekStart)
				var temperature_i int16
				err := binary.Read(buf, binary.BigEndian, &temperature_i)
				temperature = float64(temperature_i) / 10
				if err != nil {
					log.Infof("binary read failed: %v on [ % X ]\n", err, l.Data)
				}
				err = binary.Read(buf, binary.LittleEndian, &humidity)
				if err != nil {
					log.Infof("binary read failed: %v on [ % X ]\n", err, l.Data)
				}
				err = binary.Read(buf, binary.LittleEndian, &battery)
				if err != nil {
					log.Infof("binary read failed: %v on [ % X ]\n", err, l.Data)
				}
				var frame byte
				buf.Seek(2, io.SeekCurrent)
				err = binary.Read(buf, binary.LittleEndian, &frame)
				if err != nil {
					log.Infof("binary read failed: %v on [ % X ]\n", err, l.Data)
				}
				if lastFrame == frame {
					log.WithFields(log.Fields{
						"frame": frame,
					}).Debug("Duplicated frame")
					continue
				}

				lastFrame = frame
				lastUpdate = time.Now()
				if !*quiet {
					log.WithFields(log.Fields{
						"temperature": temperature,
						"humidity":    humidity,
						"battery":     battery,
						"frame":       frame,
					}).Info("Update")
				}
			}
		}
	}
	ctx := ble.WithSigHandler(context.WithCancel(context.Background()))
	go func() {
		err := ble.Scan(ctx, true, handleAdv, filter)
		if err != nil {
			log.Fatal(err)
		}
	}()
	return ctx.Done()
}

func startScan() {
	handleAdv := func(a ble.Advertisement) {
		log.WithFields(log.Fields{
			"addr": a.Addr(),
			"name": a.LocalName(),
		}).Info("Device")
	}
	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), *sd))
	err := ble.Scan(ctx, false, handleAdv, nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && os.IsTimeout(err) {
			return
		}
		log.Fatal(err)
	}
}

func startWeb() {
	log.Info(`Up and running!
	curl http://` + *webBind + `/
To see the data.
`)
	if advertisementMode {
		http.HandleFunc("/", advertisementHttpHandler)
	} else {
		http.HandleFunc("/", httpHandler)
	}
	log.Fatal(http.ListenAndServe(*webBind, nil))
}

func advertisementHttpHandler(w http.ResponseWriter, r *http.Request) {
	// avoid overhead of JSON marshalling when output is so simple!
	fmt.Fprintf(w, `{
	"temperature": %0.2f,
	"humidity": %d,
	"battery": %v,
	"lastUpdate": "%v"
}
`, temperature, humidity, battery, lastUpdate.Format("2006-01-02T15:04:05-0700"))
}

func httpHandler(w http.ResponseWriter, r *http.Request) {
	// avoid overhead of JSON marshalling when output is so simple!
	fmt.Fprintf(w, `{
	"temperature": %0.2f,
	"humidity": %d,
	"connected": %v,
	"lastUpdate": "%v"
}
`, temperature, humidity, isConnected, lastUpdate.Format("2006-01-02T15:04:05-0700"))
}

func findTemperatureCharacteristic(cln ble.Client, p *ble.Profile) *ble.Characteristic {
	for _, s := range p.Services {
		for _, c := range s.Characteristics {
			if (c.Property & ble.CharNotify) != 0 {
				for _, d := range c.Descriptors {
					if d.Handle == 0x38 {
						return c
					}
				}
			}
		}
	}
	return nil
}

func subscribe(cln ble.Client, c *ble.Characteristic) error {
	log.Info("\n-- Subscribed notification --\n")
	h := func(req []byte) {
		lastUpdate = time.Now()
		buf := bytes.NewReader(req)
		var temperature_i int16
		err := binary.Read(buf, binary.LittleEndian, &temperature_i)
		if err != nil {
			log.Errorf("binary read failed: %v on [ % X ]\n", err, req)
		}
		err = binary.Read(buf, binary.LittleEndian, &humidity)
		if err != nil {
			log.Errorf("binary read failed: %v on [ % X ]\n", err, req)
		}
		temperature = float64(temperature_i) / 100
		if !*quiet {
			log.WithFields(log.Fields{
				"temperature": temperature,
				"humidity":    humidity,
			}).Info("Update")
		}
	}
	if err := cln.Subscribe(c, false, h); err != nil {
		log.Fatalf("subscribe failed: %s", err)
		return err
	}
	return nil
}
