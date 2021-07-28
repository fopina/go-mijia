# go-mijia
Xiaomi Mijia CLI + web app

How to read is based on [this post](http://www.d0wn.com/using-bash-and-gatttool-to-get-readings-from-xiaomi-mijia-lywsd03mmc-temperature-humidity-sensor/) and [go-ble and it's explorer example](https://github.com/go-ble/ble/blob/master/examples/basic/explorer/main.go#L121-L132) which includes subscribing a notification.

## Usage

Find your Mijia MAC address with `hcitool`

```
$ hcitool lescann
LE Scan ...
B0:52:16:BB:6F:A0 (unknown)
(...)
A4:C1:38:99:99:99 LYWSD03MMC
```

Then launch `go-mijia`

```
$ ./go-mijia -addr A4:C1:38:99:99:99
Scanning for 15s...
Discovering profile...

-- Subscribed notification --
Temperature:  24.57
Humidity:     67
...
```

If using the docker image, make sure to create the container with `--net=host` and `--cap-add=NET_ADMIN`:

```
$ docker run --rm --net=host  --cap-add=NET_ADMIN fopina/go-mijia -addr A4:C1:38:99:99:99
```

Use `-web` to enable the http server (and `-web-bind` to choose the local interface and port to use). Also throw in `-quiet` option if you only want to use the HTTP endpoint and don't care for data in the output

```
$ docker run -d --rm --net=host --cap-add=NET_ADMIN fopina/go-mijia -addr A4:C1:38:99:99:99 -web -web-bind 0.0.0.0:8989 -quiet
$ curl localhost:8989
{
		"temperature": 24.55,
		"humidity": 67
}
```

Sample [home-assistant](https://www.home-assistant.io/) sensor configuration for this endpoint (as it is my own use case):

```
  - platform: rest
    name: livingroom_mijia
    resource: http://my.pi.next.to.mijia:8989/
    json_attributes:
      - temperature
      - humidity
    value_template: "OK"
  - platform: template
    sensors:
      livingroom_temperature:
        value_template: "{{ state_attr('sensor.livingroom_mijia', 'temperature') }}"
        device_class: temperature
        unit_of_measurement: "°C"
      livingroom_humidity:
        value_template: "{{ state_attr('sensor.livingroom_mijia', 'humidity') }}"
        device_class: humidity
        unit_of_measurement: "%"
```
