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
	"github.com/tve/devices/spimux"
	"github.com/tve/devices/sx1231"
	"github.com/tve/devices/sx1276"
	"periph.io/x/periph/conn/gpio"
	"periph.io/x/periph/conn/spi"
	"periph.io/x/periph/host"
)

type LogPrintf func(format string, v ...interface{})

//===== Config file structure

// Config is the top level of the config file and holds all sections.
type Config struct {
	Debug  bool
	Help   bool
	Mqtt   MqttConfig
	Radio  []RadioConfig
	Module []ModuleConfig
}

// MqttConfig holds the info from the MQTT configuration section.
type MqttConfig struct {
	Host     string
	Port     int
	User     string
	Password string
}

// RadioConfig holds the info from one radio config section. Multiple sections
// may be used to configure multiple radios.
type RadioConfig struct {
	Type       string // fsk or lora
	Prefix     string // mqtt topic prefix, /rx and /tx added
	SpiBus     int    `toml:"spi_bus"`      // SPI bus number
	SpiCS      int    `toml:"spi_cs"`       // SPI chip select number
	CSMuxPin   string `toml:"cs_mux_pin"`   // special extra chip select
	CSMuxValue int    `toml:"cs_mux_value"` // value of chip select mux
	IntrPin    string `toml:"intr_pin"`     // name of interrupt GPIO pin
	Freq       int    // radio frequency to operate at, in Mhz, Khz, or Hz
	Sync       string // sync bytes
	Rate       string // data rate name, from radio driver
	Power      int    // TX power level, in dBm
}

// ModuleConfig holds the info from one protocol module section. Multiple sections
// may be used to instatiate multiple protcol modules.
type ModuleConfig struct {
	Name string // name of module (identifies the code for it)
	Sub  string // mqtt topic to subscribe to
	Pub  string // mqtt topic to publish to
	//Offset int //
	//Value  int
	//Mask   int
}

//===== Main code

// main reads the config, connects to the MQTT broker, then sets-up the radios, sets-up
// the protocol modules, and finally lets the traffic flow.
func main() {
	// Command-line flags.
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

	// Process the config file.
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

	// Connect to MQTT broker.
	log.Printf("Connecting to MQTT broker")
	mq, err := newMQ(config.Mqtt, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to MQTT broker: %s", err)
		os.Exit(2)
	}

	// Start the HW peripheral interface library.
	log.Printf("Configuring radio(s)")
	if _, err = host.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init I/O: %s", err)
		os.Exit(2)
	}

	// Configure Radios.
	//
	// We keep a map of unused muxed SPI devices. Basically when the first radio uses a
	// muxed SPI chip select the remainder is entered here so the other radio gets it from
	// here.
	muxes := map[string]spi.Conn{}
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

// muxedSPI opens an SPI bus and uses an extra pin to mux it across two radios.
func muxedSPI(selPinName string) ([]spi.Conn, error) {
	selPin := gpio.ByName(selPinName)
	if selPin == nil {
		return nil, fmt.Errorf("cannot open pin %s", selPinName)
	}

	spiBus, err := spi.New(-1, 0)
	if err != nil {
		return nil, err
	}

	radio0, radio1 := spimux.New(spiBus, selPin)
	return []spi.Conn{radio0, radio1}, nil
}
