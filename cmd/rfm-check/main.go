// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"log"

	"github.com/google/periph/conn/gpio"
	"github.com/google/periph/conn/spi"
	"github.com/google/periph/host"
	"github.com/tve/devices/spimux"
)

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	_, err := host.Init()
	panicIf(err)

	intrPinName := "EINT17"
	//intrPinName := "1023"
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
	spiBus.Configure(spi.Mode0, 8)
	spiBus.Speed(1000000)

	spi69, spi96 := spimux.New(spiBus, selPin)

	log.Printf("Checking rfm69...")
	var r [2]byte
	err = spi69.Tx([]byte{0x01, 0}, r[:])
	panicIf(err)
	log.Printf("  op-mode is %#x", r[1])
	err = spi69.Tx([]byte{0x10, 0}, r[:])
	panicIf(err)
	switch r[1] {
	case 0x23:
		log.Printf("  found sx1231: OK!")
	case 0x24:
		log.Printf("  found sx1231h: OK!")
	default:
		log.Printf("  oops, got %#x instead of 0x23", r[1])
	}

	log.Printf("Checking rfm96 (LoRA)...")
	err = spi96.Tx([]byte{0x01, 0}, r[:])
	panicIf(err)
	log.Printf("  op-mode is %#x", r[1])
	err = spi96.Tx([]byte{0x42, 0}, r[:])
	panicIf(err)
	if r[1] == 0x12 {
		log.Printf("  found sx1276: OK!")
	} else {
		log.Printf("  oops, got %#x instead of 0x12", r[1])
	}

}
