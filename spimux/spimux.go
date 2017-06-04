// Copyright 2017 by Thorsten von Eicken, see LICENSE file

package spimux

import (
	"errors"
	"sync"

	"periph.io/x/periph/conn"
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
	mu     *sync.Mutex // prevent concurrent access to shared SPI bus
	conn   *spi.Conn   // the underlying SPI bus with shared chip select
	port   spi.Port
	selPin gpio.PinIO // pin to select between two devices
	sel    gpio.Level // select value for this device
}

// New returns two connections for the provided SPI Conn, the first one using Low for the
// select pin, and the second using High.
func New(port spi.PortCloser, selPin gpio.PinIO) (*Conn, *Conn) {
	mu := sync.Mutex{} // shared mutex
	var conn spi.Conn  // shared spi.Conn
	return &Conn{&mu, &conn, port, selPin, gpio.Low}, &Conn{&mu, &conn, port, selPin, gpio.High}
}

// DevParams sets the device parameters and returns itself ('cause it's a Port as well as a Conn).
func (c *Conn) DevParams(maxHz int64, mode spi.Mode, bits int) (spi.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if *c.conn == nil {
		conn, err := c.port.DevParams(maxHz, mode, bits)
		if err != nil {
			return nil, err
		}
		*c.conn = conn
	}

	return c, nil
}

// Tx sets the select pin to the correct value and calls the underlying Tx.
func (c *Conn) Tx(w, r []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.selPin.Out(c.sel)
	return (*c.conn).Tx(w, r)
}

/* Write sets the select pin to the correct value and calls the underlying Write.
func (c *Conn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return (*c.conn).Write(b)
} */

// Close is a no-op. TODO: close once both spimux are closed.
func (c *Conn) Close() error { return nil }

// Duplex implements the spi.Conn interface.
func (c *Conn) Duplex() conn.Duplex { return conn.Full }

// TxPackets is not implemented.
func (c *Conn) TxPackets(p []spi.Packet) error { return errors.New("TxPackets is not implemented") }

// LimitSpeed is not implemented.
func (c *Conn) LimitSpeed(maxHz int64) error { return errors.New("limitSpeed is not implemented") }

var _ spi.Conn = &Conn{}
var _ spi.PortCloser = &Conn{}
