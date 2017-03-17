// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/tve/devices/spimux"
	rfm69 "github.com/tve/devices/sx1231"
	"periph.io/x/periph/conn/gpio"
	"periph.io/x/periph/conn/spi"
	"periph.io/x/periph/host"
)

func run(intrPinName, csPinName string, csVal, power int, debug bool) error {
	if _, err := host.Init(); err != nil {
		return err
	}

	intrPin := gpio.ByName(intrPinName)
	if intrPin == nil {
		return fmt.Errorf("cannot open pin %s", intrPinName)
	}

	selPin := gpio.ByName(csPinName)
	if selPin == nil {
		return fmt.Errorf("cannot open pin %s", csPinName)
	}

	spiBus, err := spi.New(-1, 0)
	if err != nil {
		return err
	}

	spi1231, b := spimux.New(spiBus, selPin)
	if csVal != 0 {
		spi1231 = b
	}

	log.Printf("Initializing sx1231...")
	t0 := time.Now()
	rfm69, err := rfm69.New(spi1231, intrPin, rfm69.RadioOpts{
		Sync:   []byte{0x2D, 0x06},
		Freq:   915750000,
		Rate:   49230,
		Logger: log.Printf,
	})
	if err != nil {
		return err
	}
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
			if rfm69.Error() != nil {
				return rfm69.Error()
			}
		}

		time.Sleep(100 * time.Millisecond)
		log.Printf("Bye...")

	} else {

		log.Printf("Receiving packets ...")
		for pkt := range rxChan {
			log.Printf("Got len=%d rssi=%ddB fei=%dHz %q",
				len(pkt.Payload), pkt.Rssi, pkt.Fei, string(pkt.Payload))
		}
	}
	return nil
}

func main() {
	intrPin := flag.String("intr", "XIO-P0", "sx1231 radio interrupt pin name")
	csPin := flag.String("cspin", "CSID0", "sx1231 radio chip select pin name")
	csVal := flag.Int("csval", 0, "sx1231 radio chip select value (0 or 1)")
	power := flag.Int("power", 15, "sx1231 radio output power in dBm (2..17)")
	debug := flag.Bool("debug", false, "enable debug output")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s:\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}
	flag.Parse()

	if err := run(*intrPin, *csPin, *csVal, *power, *debug); err != nil {
		fmt.Fprintf(os.Stderr, "Exiting due to error: %s\n", err)
		os.Exit(2)
	}
}
