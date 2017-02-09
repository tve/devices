// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/periph/conn/gpio"
	"github.com/google/periph/conn/spi"
	"github.com/google/periph/host"
	rfm69 "github.com/tve/devices/sx1231"
)

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	_, err := host.Init()
	panicIf(err)
	intrPinName := "XIO-P0"
	intrPin := gpio.ByName(intrPinName)
	if intrPin == nil {
		panic("Cannot open pin " + intrPinName)
	}
	spiBus, err := spi.New(-1, 0)
	panicIf(err)

	/* little test of blinking LED
	panicIf(embd.SetDirection("gpio6", embd.Out))
	on := 0
	for i := 0; i < 10; i++ {
		panicIf(embd.DigitalWrite("gpio6", on))
		on = 1 - on
		time.Sleep(250 * time.Millisecond)
	}
	*/

	log.Printf("Initializing RFM69...")
	t0 := time.Now()
	/*rfm69, err := rfm69.New(spiBus, intrPin, rfm69.RadioOpts{
		Sync:   []byte{0x2D, 0x96},
		Freq:   915750000,
		Rate:   49230,
		Logger: log.Printf,
	})*/
	rfm69, err := rfm69.New(spiBus, intrPin, rfm69.RadioOpts{
		Sync:   []byte{0x2D, 0x06},
		Freq:   915750000,
		Rate:   49230,
		Logger: log.Printf,
	})
	panicIf(err)
	rxChan, txChan := rfm69.RxChan, rfm69.TxChan
	log.Printf("Ready (%.1fms)", time.Since(t0).Seconds()*1000)

	if len(os.Args) > 1 && os.Args[1] == "tx" {

		for i := 1; i <= 2; i++ {
			log.Printf("Sending packet %d ...", i)
			t0 = time.Now()
			if i&1 == 0 {
				rfm69.SetPower(0x18)
			} else {
				rfm69.SetPower(0x1F)
			}
			//msg := "\x01Hello there, these are 60 chars............................"
			msg := fmt.Sprintf("\x01Hello %03d", i)
			txChan <- []byte(msg)
			log.Printf("Sent in %.1fms", time.Since(t0).Seconds()*1000)
			time.Sleep(100 * time.Millisecond)
			panicIf(rfm69.Error())
		}

		time.Sleep(100 * time.Millisecond)
		log.Printf("Bye...")

	} else {

		log.Printf("Receiving packets ...")
		for pkt := range rxChan {
			crc := "bad"
			if pkt.CrcOK {
				crc = "OK"
			}
			log.Printf("Got len=%d crc=%s rssi=%ddB fei=%dHz %q",
				len(pkt.Payload), crc, pkt.Rssi, pkt.Fei, string(pkt.Payload))
		}
	}
}
