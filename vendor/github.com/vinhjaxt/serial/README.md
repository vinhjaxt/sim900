# Forked from https://github.com/argandas/serial

# Serial

Golang package for serial port

[![GoDoc](http://godoc.org/github.com/argandas/serial?status.svg)](http://godoc.org/github.com/argandas/serial)

A Go package that allow you to read and write from the serial port.

This is a forked repo written by [@tarm](github.com/tarm).

## Install
`go get github.com/vinhjaxt/serial`

## Usage

```go
package main
 
import (
	"time"
	"github.com/vinhjaxt/serial"
)

func main() {
    sp := serial.New()
    err := sp.Open("COM1", 9600)
    if err != nil {
        panic(err)
    }
    defer sp.Close()
    sp.Println("AT")
    sp.WaitForRegexTimeout("OK.*", time.Second * 10)
}
```

## NonBlocking Mode

By default the returned serial port reads in blocking mode. Which means `Read()` will block until at least one byte is returned. If that's not what you want, specify a positive ReadTimeout and the Read() will timeout returning 0 bytes if no bytes are read.  Please note that this is the total timeout the read operation will wait and not the interval timeout between two bytes.

```go
	sp := serial.New()
    err := sp.Open("COM1", 9600, time.Second * 5)
```
