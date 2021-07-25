package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/examples/lib/dev"
)

var (
	device = flag.String("device", "default", "implementation of ble")
	name   = flag.String("name", "Gopher", "name of remote peripheral")
	addr   = flag.String("addr", "", "address of remote peripheral (MAC on Linux, UUID on OS X)")
	sub    = flag.Duration("sub", 0, "subscribe to notification and indication for a specified period, 0 for indefinitely")
	sd     = flag.Duration("sd", 15*time.Second, "scanning duration, 0 for indefinitely")
)

func main() {
	flag.Parse()

	d, err := dev.NewDevice(*device)
	if err != nil {
		log.Fatalf("can't new device : %s", err)
	}
	ble.SetDefaultDevice(d)

	// Default to search device with name of Gopher (or specified by user).
	filter := func(a ble.Advertisement) bool {
		return strings.ToUpper(a.LocalName()) == strings.ToUpper(*name)
	}

	// If addr is specified, search for addr instead.
	if len(*addr) != 0 {
		filter = func(a ble.Advertisement) bool {
			return strings.ToUpper(a.Addr().String()) == strings.ToUpper(*addr)
		}
	}

	// Scan for specified durantion, or until interrupted by user.
	fmt.Printf("Scanning for %s...\n", *sd)
	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), *sd))
	cln, err := ble.Connect(ctx, filter)
	if err != nil {
		log.Fatalf("can't connect : %s", err)
	}

	// Make sure we had the chance to print out the message.
	done := make(chan struct{})
	// Normally, the connection is disconnected by us after our exploration.
	// However, it can be asynchronously disconnected by the remote peripheral.
	// So we wait(detect) the disconnection in the go routine.
	go func() {
		<-cln.Disconnected()
		fmt.Printf("[ %s ] is disconnected \n", cln.Addr())
		close(done)
	}()

	/*
			Characteristic: ebe0ccc17a0a4b0c8a1a6ff2997da3a6 , Property: 0x12 (NR), Handle(0x35), VHandle(0x36)
		        Descriptor: 2901 Characteristic User Description, Handle(0x37)
		        Value         54656d706572617475726520616e642048756d696469 | "Temperature and Humidi"
		        Descriptor: 2902 Client Characteristic Configuration, Handle(0x38)
		        Value         0000 | "\x00\x00"
	*/

	fmt.Printf("Discovering profile...\n")
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

	// Disconnect the connection. (On OS X, this might take a while.)
	fmt.Printf("Disconnecting [ %s ]... (this might take up to few seconds on OS X)\n", cln.Addr())
	cln.CancelConnection()

	<-done
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
	fmt.Printf("\n-- Subscribed notification --\n")
	h := func(req []byte) {
		buf := bytes.NewReader(req)
		var temperature int16
		var humidity byte
		err := binary.Read(buf, binary.LittleEndian, &temperature)
		if err != nil {
			fmt.Printf("binary read failed: %v on [ % X ]\n", err, req)
		}
		err = binary.Read(buf, binary.LittleEndian, &humidity)
		if err != nil {
			fmt.Printf("binary read failed: %v on [ % X ]\n", err, req)
		}
		fmt.Println("Temperature: ", float64(temperature)/100)
		fmt.Println("Humidity:    ", humidity)
	}
	if err := cln.Subscribe(c, false, h); err != nil {
		log.Fatalf("subscribe failed: %s", err)
	}
	defer func() {
		if err := cln.Unsubscribe(c, false); err != nil {
			log.Fatalf("unsubscribe failed: %s", err)
		}
		fmt.Printf("-- Unsubscribe to notification --\n")
	}()

	if *sub == 0 {
		for {
			time.Sleep(time.Hour)
		}
	} else {
		time.Sleep(*sub)
	}
	return nil
}
