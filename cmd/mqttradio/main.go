// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kidoman/embd"
	_ "github.com/kidoman/embd/host/chip"
	"github.com/tve/devices"
	"github.com/tve/devices/spimux"
	"github.com/tve/devices/sx1231"
	"github.com/tve/devices/sx1276"
)

type LogPrintf func(format string, v ...interface{})

// run does some massaging of the options, instantiates the appropriate radio object,
// and then calls the appropriate gateway function.
func run(spiDev devices.SPI, intrPinName, rate string, power, freq int, syncStr string,
	tx <-chan message, rx chan<- message, logger LogPrintf,
) error {
	// intrPin := gpio.ByName(intrPinName)
	intrPin := devices.NewGPIO(intrPinName)
	if intrPin == nil {
		return fmt.Errorf("cannot open pin %s", intrPinName)
	}

	if strings.HasPrefix(rate, "lora") {
		log.Printf("Initializing LoRA radio for %s (%s)", rate, sx1276.Configs[rate].Info)
		sy, err := strconv.ParseUint(syncStr, 0, 8)
		if err != nil {
			return fmt.Errorf("cannot parse sync byte %s: %s", syncStr, err)
		}
		radio, err := sx1276.New(spiDev, intrPin, sx1276.RadioOpts{
			Sync:   byte(sy),
			Freq:   uint32(freq),
			Config: rate,
			Logger: sx1276.LogPrintf(logger),
		})
		if err != nil {
			return err
		}
		radio.SetPower(byte(power))
		log.Printf("LoRa radio ready")

		loraGW(radio, tx, rx, logger)
	} else {
		log.Printf("Initializing FSK radio for %s", rate)
		r, err := strconv.ParseUint(strings.TrimPrefix(rate, "fsk."), 10, 32)
		if err != nil {
			return fmt.Errorf("cannot parse rate %s: %s", rate, err)
		}
		sy, err := strconv.ParseUint(syncStr, 0, 8)
		if err != nil {
			return fmt.Errorf("cannot parse sync byte %s: %s", syncStr, err)
		}
		sync := []byte{}
		for sy > 0 {
			sync = append([]byte{byte(sy)}, sync...)
			sy = sy >> 8
		}
		radio, err := sx1231.New(spiDev, intrPin, sx1231.RadioOpts{
			Sync:   sync,
			Freq:   uint32(freq),
			Rate:   uint32(r),
			Logger: sx1231.LogPrintf(logger),
		})
		if err != nil {
			return err
		}
		radio.SetPower(byte(power))
		log.Printf("FSK radio ready")

		fskGW(radio, tx, rx, logger)
	}

	return nil
}

// muxedSPI opens an SPI bus and uses an extra pin to mux it across two radios.
func muxedSPI(selPinName string) (devices.SPI, devices.SPI, error) {
	// selPin := gpio.ByName(selPinName)
	selPin := devices.NewGPIO(selPinName)
	if selPin == nil {
		return nil, nil, fmt.Errorf("cannot open pin %s", selPinName)
	}

	/* periph
	spiBus, err := spi.New(-1, 0)
	if err != nil {
		return err
	} */
	spiBus := devices.NewSPI()

	radio0, radio1 := spimux.New(spiBus, selPin)
	return radio0, radio1, nil
}

func main() {
	csPin := flag.String("cspin", "", "chip select mux pin name")
	debug := flag.Bool("debug", false, "enable debug output")

	mqttHost := flag.String("mqtt", "core.voneicken.com:1883", "host:port of MQTT broker")

	intr0 := flag.String("intr0", "XIO-P0", "interrupt pin name for radio 0")
	rate0 := flag.String("rate0", "", "modulation and rate for radio 0")
	power0 := flag.Int("power0", 17, "output power in dBm for radio 0")
	freq0 := flag.Int("freq0", 915750, "center frequency in any unit for radio 0")
	sync0 := flag.String("sync0", "0x2D06", "sync word for radio 0")
	pref0 := flag.String("pref0", "radio/0", "MQTT topic prefix for radio 0")

	intr1 := flag.String("intr1", "XIO-P1", "interrupt pin name for radio 1")
	rate1 := flag.String("rate1", "", "modulation and rate for radio 1")
	power1 := flag.Int("power1", 17, "output power in dBm for radio 1")
	freq1 := flag.Int("freq1", 432600, "center frequency in any unit for radio 1")
	sync1 := flag.String("sync1", "0x12", "sync word for radio 1")
	pref1 := flag.String("pref1", "radio/1", "MQTT topic prefix for radio 1")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s:\n", os.Args[0])
		flag.PrintDefaults()

		fmt.Fprintf(os.Stderr, "Valid data rates for FSK:")
		for r := range sx1231.Rates {
			fmt.Fprintf(os.Stderr, " fsk.%d", r)
		}
		fmt.Fprint(os.Stderr, "\n")

		fmt.Fprintf(os.Stderr, "Valid modulation/rates for LoRa:\n")
		configs := make([]string, 0, len(sx1276.Configs))
		for r := range sx1276.Configs {
			configs = append(configs, r)
		}
		sort.Slice(configs, func(i, j int) bool {
			return sx1276.Configs[configs[i]].Info < sx1276.Configs[configs[j]].Info
		})
		for _, c := range configs {
			fmt.Fprintf(os.Stderr, "  %-20s: %s\n", c, sx1276.Configs[c].Info)
		}

		os.Exit(1)
	}
	flag.Parse()

	if *rate0 == "" && *rate1 == "" {
		fmt.Fprintf(os.Stderr, "-rate0 or -rate1 must be specified")
		os.Exit(1)
	}

	var logger LogPrintf
	if *debug {
		logger = log.Printf
	}

	mq, err := newMQ(*mqttHost, *pref0, *pref1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to MQTT broker: %s", err)
		os.Exit(2)
	}

	log.Printf("Opening radio")
	embd.InitGPIO()
	embd.InitSPI()
	/* periph
	_, err := host.Init()
	if err != nil {
		return err
	}*/

	var radio0, radio1 devices.SPI
	if *csPin != "" {
		radio0, radio1, err = muxedSPI(*csPin)
	} else {
		radio0 = devices.NewSPI()
	}

	if err == nil && radio0 != nil && *rate0 != "" {
		err = run(radio0, *intr0, *rate0, *power0, *freq0, *sync0,
			mq.tx0, mq.rx0, logger)
	}
	if err == nil && radio1 != nil && *rate1 != "" {
		err = run(radio1, *intr1, *rate1, *power1, *freq1, *sync1,
			mq.tx1, mq.rx1, logger)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Exiting due to error: %s\n", err)
		os.Exit(2)
	}
	log.Printf("Gateway is ready")
	for {
		time.Sleep(time.Hour)
	} // ugh!
}
