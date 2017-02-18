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
	"github.com/tve/devices/spimux"
	"github.com/tve/devices/sx1276"
)

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	_, err := host.Init()
	panicIf(err)

	//intrPinName := "EINT17"
	intrPinName := "XIO-P1"
	intrPin := gpio.ByName(intrPinName)
	if intrPin == nil {
		panic("Cannot open pin " + intrPinName)
	}

	selPinName := ""
	selPin := gpio.ByName("CSID0")
	if selPin == nil {
		panic("Cannot open pin " + selPinName)
	}

	spiBus, err := spi.New(-1, 0)
	panicIf(err)

	_, spi1276 := spimux.New(spiBus, selPin)

	log.Printf("Initializing LoRA radio...")
	t0 := time.Now()
	radio, err := sx1276.New(spi1276, intrPin, sx1276.RadioOpts{
		Sync:   0xCB,
		Freq:   432600000,
		Config: "bw62cr46sf9",
		Logger: nil, //log.Printf,
	})
	panicIf(err)
	rxChan, txChan := radio.RxChan, radio.TxChan
	log.Printf("Ready (%.1fms)", time.Since(t0).Seconds()*1000)

	if len(os.Args) > 1 && os.Args[1] == "tx" {

		for i := 1; i <= 100000; i++ {
			db := byte(20 - (i & 15))
			log.Printf("Sending packet %d @%ddBm...", i, db)
			radio.SetPower(db)
			t0 = time.Now()
			//msg := "\x01Hello there, these are 60 chars............................"
			msg := fmt.Sprintf("\x01Hello @%02ddBm %03d", db, i)
			txChan <- []byte(msg)
			//log.Printf("Sent in %.1fms", time.Since(t0).Seconds()*1000)
			time.Sleep(500 * time.Millisecond)
			panicIf(radio.Error())
		}

		time.Sleep(100 * time.Millisecond)
		log.Printf("Bye...")

	} else {

		log.Printf("Receiving packets ...")
		radio.SetPower(17)
		for pkt := range rxChan {
			log.Printf("Got len=%d snr=%ddB rssi=%ddBm fei=%dHz lna=%d %q",
				len(pkt.Payload), pkt.Snr, pkt.Rssi, pkt.Fei, pkt.Lna, string(pkt.Payload))
		}
	}
}
