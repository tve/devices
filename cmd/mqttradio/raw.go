// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"fmt"
	"log"
	"strconv"
	"time"

	_ "github.com/kidoman/embd/host/chip"
	"github.com/tve/devices"
	"github.com/tve/devices/sx1231"
	"github.com/tve/devices/sx1276"
)

// RawRxPacket is the structure published to MQTT for raw packets received on a radio.
type RawRxPacket struct {
	Packet []byte    `json:"packet"` // packet, including headers, excl sync, length, CRC
	Rssi   int       `json:"rssi"`   // RSSI in dB for packet, 0 if unknown
	Snr    int       `json:"snr"`    // Signal to noise in dB, 0 if unknown (ugh)
	Fei    int       `json:"fei"`    // Freq error in Hz for packet, 0 if unknown (ugh)
	At     time.Time `json:"at"`     // time of recv interrupt
}

// RawRxMessage is the full MQTT message for a RawRxPacket. Primarily used when subscribing
// internally to a raw Rx topic.
type RawRxMessage struct {
	Topic   string
	Payload RawRxPacket
}

// RawTxPacket is the payload expected via MQTT for raw packets to be transmitted on a radio.
// It is a struct for symmetry with RawRxPacket and to allow more fields to be added in the
// future as needed.
type RawTxPacket struct {
	Packet []byte `json:"packet"` // packet, including headers, excl sync, length, CRC
}

// RawTxMessage is the full MQTT message for a RawTxPacket.
type RawTxMessage struct {
	Topic   string
	Payload RawTxPacket
}

// startRadio prepares all the devices, pins, and MQTT channels needed to operate a radio
// and then calls the radio type specific function to start the gatewaying goroutines.
func startRadio(r RadioConfig, muxes map[string]devices.SPI, mq *mq, debug LogPrintf) error {
	if debug != nil {
		debug("Configuring radio for %s: %+v", r.Prefix, r)
	}

	// First step is to get a handle onto the SPI device. Need to deal with muxed
	// devices, though.
	if r.SpiBus != 0 || r.SpiCS != 0 {
		return fmt.Errorf("Sorry, only SPI bus 0, chip select 0 supported")
	}
	var dev devices.SPI
	if r.CSMuxPin == "" {
		// Easy case: non-muxed SPI bus.
		dev = devices.NewSPI()
	} else {
		// More complex: SPI bus with muxed chip select.
		// muxKey indexes into the muxes hash to locate existing SPI mux devices.
		muxKey := func(bus, cs int, muxPin string, muxValue int) string {
			return fmt.Sprintf("%d:%d:%s:%d", bus, cs, muxPin, muxValue)
		}
		k := muxKey(r.SpiBus, r.SpiCS, r.CSMuxPin, r.CSMuxValue)
		dev = muxes[k]
		if dev == nil {
			// Need to open a muxed bus.
			if r.CSMuxValue < 0 || r.CSMuxValue > 1 {
				return fmt.Errorf("Sorry, CSMuxValue must be 0 or 1")
			}
			d, err := muxedSPI(r.CSMuxPin)
			if err != nil {
				return fmt.Errorf("Error opening SPI: %s", err)
			}
			// Save the device we're not using for later.
			k := muxKey(r.SpiBus, r.SpiCS, r.CSMuxPin, 1-r.CSMuxValue)
			muxes[k] = d[1-r.CSMuxValue]
			dev = d[r.CSMuxValue]
		}
	}

	// Create MQTT subscription for Tx.
	txChan := make(chan RawTxMessage, 10)
	if err := mq.Subscribe(r.Prefix+"/tx", txChan); err != nil {
		return err
	}

	// Create MQTT publisher with prefix for rx.
	rxPub := func(pkt *RawRxPacket) { mq.Publish(r.Prefix+"/rx", pkt) }

	// Open the interrupt pin.
	intrPin := devices.NewGPIO(r.IntrPin)
	// intrPin := gpio.ByName(intrPinName)
	if intrPin == nil {
		return fmt.Errorf("cannot open pin %s", r.IntrPin)
	}

	// Parse the sync word string into a byte array.
	sy, err := strconv.ParseUint(r.Sync, 0, 64)
	if err != nil {
		return fmt.Errorf("cannot parse sync bytes %s: %s", r.Sync, err)
	}
	sync := []byte{}
	for sy > 0 {
		sync = append([]byte{byte(sy)}, sync...)
		sy = sy >> 8
	}

	rs := &radioSettings{dev: dev, intrPin: intrPin, freq: uint32(r.Freq),
		rate: r.Rate, sync: sync, power: r.Power}

	switch r.Type {
	case "lora.sx1276":
		err = lora1276GW(rs, r.Prefix, txChan, rxPub, debug)
	case "fsk.rfm69":
		err = fsk69GW(rs, false, r.Prefix, txChan, rxPub, debug)
	case "fsk.rfm69h":
		err = fsk69GW(rs, true, r.Prefix, txChan, rxPub, debug)
	default:
		err = fmt.Errorf("unknown radio type: %s", r.Type)
	}
	return err
}

// radioSettings contains the settings of a radio.
type radioSettings struct {
	dev     devices.SPI  // radio device interface
	intrPin devices.GPIO // interrupt pin
	freq    uint32       // center frequency
	rate    string       // name for modulation/data-rate setting
	sync    []byte       // sync bytes
	power   int          // output power in dBm
}

// lora1276GW instantiates an sx1276 radio in LoRa mode, and then gateways
// between the radio and mqtt.
func lora1276GW(conf *radioSettings, prefix string, txChan <-chan RawTxMessage,
	rxPub func(*RawRxPacket), debug LogPrintf,
) error {

	log.Printf("Initializing LoRA sx1276 radio for %s", prefix)
	radio, err := sx1276.New(conf.dev, conf.intrPin, sx1276.RadioOpts{
		Sync:   conf.sync[0],
		Freq:   conf.freq,
		Config: conf.rate,
		Logger: sx1276.LogPrintf(debug),
	})
	if err != nil {
		return err
	}
	radio.SetPower(byte(conf.power))
	log.Printf("LoRa radio ready")

	// Radio -> MQTT goroutine.
	go func() {
		for pkt := range radio.RxChan {
			rxPub(&RawRxPacket{Packet: pkt.Payload, Rssi: pkt.Rssi, Fei: pkt.Fei, At: pkt.At})
		}
		log.Printf("%s: radio->mqtt goroutine exiting", prefix)
	}()

	// MQTT -> Radio goroutine
	go func() {
		for pkt := range txChan {
			radio.TxChan <- pkt.Payload.Packet
		}
		log.Printf("%s: mqtt->radio goroutine exiting", prefix)
	}()
	return nil
}

// fsk69GW instantiates an sx1231 radio, and then gateways between the radio and mqtt.
// If paBoost is true then power amplifiers PA1 and PA2 are used, else PA0 is used.
func fsk69GW(conf *radioSettings, paBoost bool, prefix string, txChan <-chan RawTxMessage,
	rxPub func(*RawRxPacket), debug LogPrintf,
) error {

	log.Printf("Initializing FSK sx1231 radio for %s", prefix)
	rate, err := strconv.ParseUint(conf.rate, 0, 32)
	if err != nil {
		return fmt.Errorf("cannot parse data rate %s: %s", conf.rate, err)
	}

	radio, err := sx1231.New(conf.dev, conf.intrPin, sx1231.RadioOpts{
		Sync:    conf.sync,
		Freq:    conf.freq,
		Rate:    uint32(rate),
		PABoost: paBoost,
		Logger:  sx1231.LogPrintf(debug),
	})
	if err != nil {
		return err
	}
	radio.SetPower(byte(conf.power))
	log.Printf("FSK radio ready")

	// Radio -> MQTT goroutine.
	go func() {
		for pkt := range radio.RxChan {
			log.Printf("%s: RX %ddBm %dHz %db: %#x",
				prefix, pkt.Rssi, pkt.Fei, len(pkt.Payload), pkt.Payload)
			rxPub(&RawRxPacket{Packet: pkt.Payload, Rssi: pkt.Rssi, Fei: pkt.Fei, At: pkt.At})
		}
		log.Printf("%s: radio->mqtt goroutine exiting", prefix)
	}()

	// MQTT -> Radio goroutine
	go func() {
		for pkt := range txChan {
			buf := pkt.Payload.Packet
			log.Printf("%s: TX %db: %#x", prefix, len(buf), buf)
			radio.TxChan <- buf
		}
		log.Printf("%s: mqtt->radio goroutine exiting", prefix)
	}()

	return nil
}
