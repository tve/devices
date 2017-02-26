// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/kidoman/embd"
	_ "github.com/kidoman/embd/host/chip"
	"github.com/tve/devices"
	"github.com/tve/devices/spimux"
	"github.com/tve/devices/sx1231"
	"github.com/tve/devices/sx1276"
)

type LogPrintf func(format string, v ...interface{})

type Config struct {
	Debug  bool
	Help   bool
	Mqtt   MqttConfig
	Radio  []RadioConfig
	Module []ModuleConfig
}

type MqttConfig struct {
	Host     string
	Port     int
	User     string
	Password string
}

type RadioConfig struct {
	Type       string
	Prefix     string
	SpiBus     int    `toml:"spi_bus"`
	SpiCS      int    `toml:"spi_cs"`
	CSMuxPin   string `toml:"cs_mux_pin"`
	CSMuxValue int    `toml:"cs_mux_value"`
	IntrPin    string `toml:"intr_pin"`
	Freq       int
	Sync       string
	Rate       string
	Power      int
}

type ModuleConfig struct {
	Name string
	Sub  string
	Pub  string
}

// muxedSPI opens an SPI bus and uses an extra pin to mux it across two radios.
func muxedSPI(selPinName string) ([]devices.SPI, error) {
	selPin := devices.NewGPIO(selPinName)
	// selPin := gpio.ByName(selPinName)
	if selPin == nil {
		return nil, fmt.Errorf("cannot open pin %s", selPinName)
	}

	spiBus := devices.NewSPI()
	/* periph
	spiBus, err := spi.New(-1, 0)
	if err != nil {
		return err
	} */

	radio0, radio1 := spimux.New(spiBus, selPin)
	return []devices.SPI{radio0, radio1}, nil
}

func main() {
	help := flag.Bool("help", false, "print usage help")
	configFile := flag.String("config", "mqttradio.toml", "path to config file")
	flag.Parse()

	if *help {
		fmt.Fprintf(os.Stderr, "Usage: %s:\n", os.Args[0])

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

	config := &Config{}
	rawConfig, err := ioutil.ReadFile(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot access config file: %s", err)
		os.Exit(1)
	}
	err = toml.Unmarshal(rawConfig, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot parse config file: %s", err)
		os.Exit(1)
	}

	if len(config.Radio) == 0 {
		fmt.Fprintf(os.Stderr, "At least one radio must be specified in the config")
		os.Exit(1)
	}

	logger := LogPrintf(func(format string, v ...interface{}) {})
	if config.Debug {
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
		logger = log.Printf
	}

	mq, err := newMQ(config.Mqtt, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to MQTT broker: %s", err)
		os.Exit(2)
	}

	log.Printf("Configuring radio(s)")
	embd.InitGPIO()
	embd.InitSPI()
	/* periph
	_, err := host.Init()
	if err != nil {
		return err
	}*/

	// Configure Radios.
	//
	// We keep a map of unused muxed SPI devices. Basically when the first radio uses a
	// muxed SPI chip select the remainder is entered here so the other radio gets it from
	// here.
	muxes := map[string]devices.SPI{}
	for _, r := range config.Radio {
		if err := startRadio(r, muxes, mq, logger); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to config radio for %s: %s", r.Prefix, err)
			os.Exit(1)
		}
	}

	log.Printf("Configuring modules")
	for _, m := range config.Module {
		if err := hookModule(m, mq, logger); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to install module %s (%s->%s): %s",
				m.Name, m.Sub, m.Pub, err)
			os.Exit(1)
		}
	}

	log.Printf("Gateway is ready")
	for {
		time.Sleep(time.Hour)
	} // ugh!
}
