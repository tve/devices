// Copyright 2017 by Thorsten von Eicken, see LICENSE file

package spimux

import (
	"sync"

	"periph.io/x/periph/conn/gpio"
	"periph.io/x/periph/conn/spi"
)

// Conn represents a connection to a device on an SPI bus with a multiplexed chip select.
//
// The purpose of spimux.Conn is to allow two devices to be connected to SPI buses
// that only have a single chip select line. This is accomplished by placing a demux
// on the CS line such that a an extra gpio pin can direct the chip select to either
// of the two devices. The way this functions is that the spimux.Conn Tx function sets
// the demux select for the appropriate device and then performs a std transaction.
//
// A sample circuit is to use an 74LVC1G19 demux with the SPI CS connected to E, the
// gpio select pin connected to A, and the CS inputs of the two devices attached to
// Y0 and Y1 respectively. A pull-down resitor on the A input of the demux is recommended
// to ensure both CS remain inactive when the SPI CS is not driven.
//
// A limitation of the current implementation is that the speed setting and the configuration
// (SPI mode and number of bits) is shared between the two devices, i.e., it is not possible
// to use different settings.
type Conn struct {
	mu       *sync.Mutex // prevent concurrent access to shared SPI bus
	spi.Conn             // the underlying SPI bus with shared chip select
	selPin   gpio.PinIO  // pin to select between two devices
	sel      gpio.Level  // select value for this device
}

// New returns two connections for the provided SPI Conn, the first one using Low for the
// select pin, and the second using High.
func New(spi spi.Conn, selPin gpio.PinIO) (*Conn, *Conn) {
	mu := sync.Mutex{}
	return &Conn{&mu, spi, selPin, gpio.Low}, &Conn{&mu, spi, selPin, gpio.High}
}

// Tx sets the select pin to the correct value and calls the underlying Tx.
func (c *Conn) Tx(w, r []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.selPin.Out(c.sel)
	return c.Conn.Tx(w, r)
}

// Write sets the select pin to the correct value and calls the underlying Write.
func (c *Conn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Conn.Write(b)
}

// Close is a no-op. TODO: close once both spimux are closed.
func (c *Conn) Close() error { return nil }
