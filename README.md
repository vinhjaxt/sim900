# Forked from https://github.com/argandas/sim900

# SIM900 Go's package
This package uses a serialport to communicate with the SIM900 GSM Modem.

## How to install

- You'll need Golang v1.3+
- SIM900 Package uses the [serial](https://github.com/vinhjaxt/serial) package in order to communicate with the modem via AT commands, you will need to install both SIM900 and serial packages.

```bash
go get github.com/vinhjaxt/serial  # installs the serial package
go get github.com/vinhjaxt/sim900  # installs the SIM900 package
```

## How to use

- You'll need an available serial port, SIM900 boards usually works with 5V TTL signals so you can get a USB-to-Serial TTL converter, I recommend you to use the [FTDI Cable](https://www.sparkfun.com/products/9718) for this, but you can use any USB-to-Serial adapters there are plenty of them. 
![SIM900: FTDI Cable](TBD)

- Connect carefuly your serialport to your SIM900 board.
![SIM900: Connection diagram](TBD)

## Example code

```go
package main

import (
	"encoding/hex"
	"log"
	"strings"
	"time"

	"github.com/vinhjaxt/sim900"
	"github.com/xlab/at/sms"
)

func main() {
	gsm := sim900.New()
	gsm.Port.Verbose = false
	// /dev/ttyUSB2
	err := gsm.Connect("COM23", 460800)
	if err != nil {
		log.Println(err)
		return
	}
	defer gsm.Disconnect()

	_, err = gsm.SendSMS("170", "KTS3")
	if err != nil {
		log.Println(err)
		return
	}

	smsID, err := gsm.WaitSMS(60 * time.Second)
	if err != nil {
		log.Println(err)
		return
	}

	text, err := gsm.ReadSMS(smsID, sim900.TEXT_MODE)
	if err != nil {
		log.Println(err)
		return
	}
	log.Printf("Text %q", text)

	pduData, err := gsm.ReadSMS(smsID, sim900.PDU_MODE)
	if err != nil {
		log.Println(err)
		return
	}
	bs, err := hex.DecodeString(strings.Trim(pduData[2], "\r\n"))
	if err != nil {
		log.Panicln(err)
		return
	}
	msg := new(sms.Message)
	msg.ReadFrom(bs)
	log.Printf("Text in PDU %q", msg.Text)

	err = gsm.ClearSMS()
	if err != nil {
		log.Println(err)
		return
	}
}
```

## Reference

- List of available SIM900 commands can be found [here](http://wm.sim.com/upfile/2013424141114f.pdf).
- For more information about available SIM900 methods please check godoc for this package.

Go explore!

## License

SIM900 package is MIT-Licensed
