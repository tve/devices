// Copyright 2016 by Thorsten von Eicken, see LICENSE file

// The SX1276 package interfaces with a HopeRF RFM95/96/97/98 LoRA radio connected to an SPI bus.
//
// The RFM9x modules use a Semtech SX1276 radio chip. This package has also been tested with a
// Dorji DRF1278 module and it should work fine with other radio modules using the same chip.
// Note that the SX1276, SX1277, SX1278, and SX1279 all function identically and only differ
// in which RF bands they support.
//
// The driver is fully interrupt driven and requires that the radio's DIO0 pin be connected to
// an interrupt capable GPIO pin. The transmit and receive interface uses a pair of tx and rx
// channels, each having a small amount of buffering.
//
// In general, other than a few user errors (such as passing too large a packet to Send) there
// should be no errors during the radio's operation unless there is a hardware failure. For this
// reason radio interface errors are treated as fatal: if such an error occurs the rx channel is
// closed and the error is recorded in the Radio struct where it can be retrieved using the Error
// function. The object will be unusable for further operation and the client code will have to
// create and initialize a fresh object which will re-establish communication with the radio chip.
//
// Limitations
//
// This driver uses the SX1276 in LoRA mode only.
//
// Only the explicit header mode is supported, this means that spreading factor 6 cannot be
// used and thus the maximum data rate available is 21875bps.
//
// The methods on the Radio object are not concurrency safe. Since they all deal with configuration
// this should not pose difficulties. The Error function may be called from multiple goroutines
// and obviously the TX and RX channels work well with concurrency.
package sx1276

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/periph/conn/gpio"
	"github.com/google/periph/conn/spi"
)

const rxChanCap = 4 // queue up to 4 received packets before dropping
const txChanCap = 4 // queue up to 4 packets to tx before blocking

// Radio represents a Semtech SX127x LoRA radio.
type Radio struct {
	TxChan chan<- []byte    // channel to transmit packets
	RxChan <-chan *RxPacket // channel for received packets
	// configuration
	spi     spi.ConnCloser // SPI device to access the radio
	intrPin gpio.PinIn     // interrupt pin for RX and TX interrupts
	intrCnt int            // count interrupts
	sync    byte           // sync byte
	//freq    uint32         // center frequency
	//config  string         // entry in Configs table being used
	// state
	sync.Mutex                // guard concurrent access to the radio
	mode       byte           // current operation mode
	err        error          // persistent error
	rxChan     chan *RxPacket // channel to push recevied packets into
	txChan     chan []byte    // channel for packets to be transmitted
	log        LogPrintf      // function to use for logging
}

// RadioOpts contains options used when initilizing a Radio.
type RadioOpts struct {
	Sync   byte      // RF sync byte
	Freq   uint32    // center frequency in Hz, Khz, or Mhz
	Config string    // entry in Configs table to use
	Logger LogPrintf // function to use for logging
}

// Config describes the SX127x configuration to achieve a specific bandwidth, spreading factor,
// and coding rate.
type Config struct {
	Conf1 byte // ModemConfig1: bw, coding rate, implicit/expl header
	Conf2 byte // ModemConfig2: sperading, mode, crc
	Conf3 byte // ModemConfig3: low data rate opt, LNA gain
}

// Configs is the table of supported configurations and their corresponding register settings.
// In order to operate at a new bit rate the table can be extended by the client.
var Configs = map[string]Config{
	// Configurations from radiohead library, the first one is fast for short range, the
	// second intermediate for medium range, and the last two slow for long range.
	"bw500cr45sf128":// 500Khz bandwidth, 4/5 coding rate, spreading fct 128 = bps
	{0x92, 0x74, 0x00},
	"bw125cr45sf128":// 125Khz bandwidth, 4/5 coding rate, spreading fct 128 = bps
	{0x72, 0x74, 0x00},
	"bw125cr48sf4096":// 125Khz bandwidth, 4/8 coding rate, spreading fct 4096 = bps
	{0x78, 0xc4, 0x00},
	"bw31cr48sf512":// 31.25Khz bandwidth, 4/8 coding rate, spreading fct 512 = bps
	{0x48, 0x94, 0x00},
}

// RxPacket is a received packet with stats.
type RxPacket struct {
	Payload []byte // payload, excluding length & crc
	Snr     int    // signal-to-noise in dB for packet
	Rssi    int    // rssi in dB for packet
	Fei     int    // frequency error for packet
}

// New initializes an sx1276 Radio given an spi.Conn and an interrupt pin, and places the radio
// in receive mode.
//
// To transmit, push packet payloads into the returned txChan.
// Received packets will be sent on the returned rxChan, which has a small amount of
// buffering. The rxChan will be closed if a persistent error occurs when
// communicating with the device, use the Error() function to retrieve the error.
func New(dev spi.ConnCloser, intr gpio.PinIn, opts RadioOpts) (*Radio, error) {
	r := &Radio{
		spi: dev, intrPin: intr,
		mode: 255,
		err:  fmt.Errorf("sx1276 is not initialized"),
		log:  func(format string, v ...interface{}) {},
	}
	if opts.Logger != nil {
		r.log = opts.Logger
	}

	// Set SPI parameters.
	if err := dev.Speed(4 * 1000 * 1000); err != nil {
		return nil, fmt.Errorf("sx1276: cannot set speed, %v", err)
	}
	if err := dev.Configure(spi.Mode0, 8); err != nil {
		return nil, fmt.Errorf("sx1276: cannot set mode, %v", err)
	}

	// Try to synchronize communication with the sx1276.
	sync := func(pattern byte) error {
		for n := 10; n > 0; n-- {
			// Doing write transactions explicitly to get OS errors.
			r.writeReg(REG_SYNC, pattern)
			if err := dev.Tx([]byte{REG_SYNC | 0x80, pattern}, []byte{0, 0}); err != nil {
				return fmt.Errorf("sx1276: %s", err)
			}
			// Read same thing back, we hope...
			v := r.readReg(REG_SYNC)
			if v == pattern {
				return nil
			}
		}
		return errors.New("sx1276: cannot sync with chip")
	}
	if err := sync(0xaa); err != nil {
		return nil, err
	}
	if err := sync(0x55); err != nil {
		return nil, err
	}

	r.setMode(MODE_SLEEP)

	// Detect chip version.
	r.log("SX1276 version %#x", r.readReg(REG_VERSION))

	// Write the configuration into the registers.
	for i := 0; i < len(configRegs)-1; i += 2 {
		r.writeReg(configRegs[i], configRegs[i+1])
	}

	// Configure the transmission parameters.
	r.SetConfig(opts.Config)
	r.SetFrequency(opts.Freq)

	//r.sync = opts.Sync
	r.spi.Tx([]byte{REG_SYNC | 0x80, opts.Sync}, []byte{0, 0})

	// Allocate channels for packets, give them some buffer but the reality is that
	// packets don't come in that fast anyway...
	r.rxChan = make(chan *RxPacket, rxChanCap)
	r.txChan = make(chan []byte, txChanCap)
	r.RxChan = r.rxChan
	r.TxChan = r.txChan

	// Initialize interrupt pin.
	if err := r.intrPin.In(gpio.Float, gpio.RisingEdge); err != nil {
		return nil, fmt.Errorf("sx1276: error initializing interrupt pin: %s", err)
	}

	// Test the interrupt function by transmitting a packet such that it generates an interrupt
	// and then call WaitForEdge. Start by verifying that we don't have any pending interrupt.
	for r.intrPin.WaitForEdge(0) {
		r.log("Interrupt test shows an incorrect pending interrupt")
	}
	// Tx a packet
	r.log("Interrupt pin is %v", r.intrPin.Read())
	r.send([]byte{0})
	if !r.intrPin.WaitForEdge(time.Second) {
		r.logRegs()
		r.log("Interrupt pin is %v", r.intrPin.Read())
		return nil, fmt.Errorf("sx1276: interrupts from radio do not work, try unexporting gpio%d", r.intrPin.Number())
	}
	r.writeReg(REG_IRQFLAGS, 0xff) // clear IRQ
	time.Sleep(10 * time.Millisecond)
	for r.intrPin.WaitForEdge(0) {
	}

	// log register contents
	r.logRegs()

	// Finally turn on the receiver.
	go r.worker()
	r.err = nil // can get an interrupt anytime now...
	r.setMode(MODE_RX_CONT)

	return r, nil
}

// SetFrequency changes the center frequency at which the radio transmits and receives. The
// frequency can be specified at any scale (hz, khz, mhz). The frequency value is not checked
// and invalid values will simply cause the radio not to work particularly well.
func (r *Radio) SetFrequency(freq uint32) {
	// accept any frequency scale as input, including KHz and MHz
	// multiply by 10 until freq >= 100 MHz
	for freq > 0 && freq < 100000000 {
		freq = freq * 10
	}

	// Frequency steps are in units of (32,000,000 >> 19) = 61.03515625 Hz
	// use multiples of 64 to avoid multi-precision arithmetic, i.e. 3906.25 Hz
	// due to this, the lower 6 bits of the calculated factor will always be 0
	// this is still 4 ppm, i.e. below the radio's 32 MHz crystal accuracy
	// 868.0 MHz = 0xD90000, 868.3 MHz = 0xD91300, 915.0 MHz = 0xE4C000
	frf := (freq << 2) / (32000000 >> 11)
	//log.Printf("SetFreq: %d %d %02x %02x %02x", freq, uint32(frf<<6),
	//	byte(frf>>10), byte(frf>>2), byte(frf<<6))
	r.setMode(MODE_STANDBY)
	r.writeReg(REG_FRFMSB, byte(frf>>10), byte(frf>>2), byte(frf<<6))
	r.log("SetFreq %dHz -> %#x %#x %#x", freq, byte(frf>>10), byte(frf>>2), byte(frf<<6))
	r.setMode(MODE_RX_CONT)
}

// SetConfig sets the modem configuration using one of the entries in the Configs table.
// If the entry specified does not exist, nothing is changed.
func (r *Radio) SetConfig(config string) {
	conf, found := Configs[config]
	if !found {
		return
	}

	r.setMode(MODE_STANDBY)
	r.writeReg(REG_MODEMCONF1, conf.Conf1&^1)        // Explicit header mode
	r.writeReg(REG_MODEMCONF2, conf.Conf2&0xf0|0x04) // TxSingle, CRC enable
	r.writeReg(REG_MODEMCONF3, conf.Conf3|0x04)      // enable LNA AGC
	r.setMode(MODE_RX_CONT)
}

// SetPower configures the radio for the specified output power. It only supports the high-power
// amp because RFM9x modules don't have the lower-power amps connected to anything.
//
// The datasheet is confusing about how PaConfig gets set and the formula for OutputPower
// looks incorrect. Fortunately Semtech provides reference code...
func (r *Radio) SetPower(dBm byte) {
	switch {
	case dBm < 2:
		dBm = 2
	case dBm > 20:
		dBm = 20
	}
	r.log("SetPower %ddBm", dBm)
	r.setMode(MODE_STANDBY)
	if dBm > 17 {
		r.writeReg(REG_PADAC, 0x07) // turn 20dBm mode on, this offsets the PACONFIG by 3
		r.writeReg(REG_PACONFIG, 0xf0+dBm-5)
	} else {
		r.writeReg(REG_PACONFIG, 0xf0+dBm-2)
		r.writeReg(REG_PADAC, 0x04)
	}
	r.setMode(MODE_RX_CONT)
}

// LogPrintf is a function used by the driver to print logging info.
type LogPrintf func(format string, v ...interface{})

// SetLogger sets a logging function, nil may be used to disable logging, which is the default.
func (r *Radio) SetLogger(l LogPrintf) {
	if l != nil {
		r.log = l
	} else {
		r.log = func(format string, v ...interface{}) {}
	}
}

// Error returns any persistent error that may have been encountered.
func (r *Radio) Error() error { return r.err }

//

// rxInfo contains stats about a received packet.
type rxInfo struct {
	rssi int // rssi value for current packet
	lna  int // low noise amp gain for current packet
	fei  int // frequency error for current packet
	afc  int // frequency correction applied for current packet
}

// setMode changes the radio's operating mode and changes the interrupt cause (if necessary).
func (r *Radio) setMode(mode byte) {
	mode = mode & 0x07

	// If we're in the right mode then don't do anything.
	if r.mode == mode {
		return
	}

	// Set the interrupt mode if necessary.
	switch mode {
	case MODE_TX:
		r.writeReg(REG_DIOMAPPING1, 0x40) // TxDone
	case MODE_RX_CONT, MODE_RX_SINGLE:
		r.writeReg(REG_DIOMAPPING1, 0x00) // RxDone
	default:
		// Mode used when switching, make sure we don't get an interupt.
		r.writeReg(REG_DIOMAPPING1, 0xc0) // No intr
	}

	// Set the new mode.
	r.writeReg(REG_OPMODE, mode+0x88) // LoRA mode & LF
	r.log("Mode %#x", mode)
	r.mode = mode
}

// receiving checks whether a reception is currently in progress.
// It also protects from a packet sitting in RX that hasn't been picked-up yet by the
// interrupt handler.
func (r *Radio) receiving() bool {
	// Can't be receiving if we're not in the right mode...
	if r.mode != MODE_RX_CONT {
		return false
	}
	st := r.readReg(REG_MODEMSTAT)
	irq := r.readReg(REG_IRQFLAGS)
	return st&MODEM_CLEAR != 0 && irq&IRQ_RXDONE == 0
}

// worker is an endless loop that processes interrupts for reception as well as packets
// enqueued for transmit.
func (r *Radio) worker() {
	// Interrupt goroutine converting WaitForEdge to a channel.
	intrChan := make(chan struct{})
	intrStop := make(chan struct{})
	go func() {
		// Make sure we're not missing an initial edge due to a race condition.
		if r.intrPin.Read() == gpio.High {
			intrChan <- struct{}{}
		}
		for {
			if r.intrPin.WaitForEdge(time.Second) {
				// CHIP does BothEdges on the XIO pins, so we get extra intrs
				if r.intrPin.Read() == gpio.High {
					r.log("interrupt")
					intrChan <- struct{}{}
				} else {
					r.log("end-of-interrupt")
				}
			} else if r.intrPin.Read() == gpio.High {
				r.log("Interrupt was missed!")
				intrChan <- struct{}{}
			} else {
				r.log("IRQ flags: %#x", r.readReg(REG_IRQFLAGS))
				select {
				case <-intrStop:
					r.log("sx1276: rx interrupt goroutine exiting")
					return
				default:
				}
			}
		}
	}()

	for r.err == nil {
		select {
		// interrupt
		case <-intrChan:
			r.intrCnt++
			// What this interrupt is about depends on the current mode.
			switch r.mode {
			case MODE_RX_CONT:
				r.intrReceive()
			case MODE_TX:
				r.setMode(MODE_RX_CONT)
			default:
				r.log("Spurious interrupt in mode=%x", r.mode)
			}
			r.writeReg(REG_IRQFLAGS, 0xff) // clear IRQ

		// Something to transmit.
		case payload := <-r.txChan:
			if r.receiving() {
				r.intrReceive() // TODO: doesn't work, need to busy-wait
			}
			if r.err == nil {
				r.send(payload)
			}
		}
	}
	r.log("sx1276: rx goroutine exiting, %s", r.err)
	// Signal to clients that something is amiss.
	close(r.rxChan)
	close(intrStop)
	r.intrPin.In(gpio.Float, gpio.NoEdge) // causes interrupt goroutine to exit
	r.spi.Close()
}

// send switches the radio's mode and starts transmitting a packet.
func (r *Radio) send(payload []byte) {
	// limit the payload to valid lengths
	if len(payload) > 250 {
		payload = payload[:250]
	}
	r.setMode(MODE_STANDBY)

	// push the message into the FIFO.
	r.writeReg(REG_FIFOPTR, 0)
	r.writeReg(REG_FIFO, payload...)
	r.writeReg(REG_PAYLENGTH, byte(len(payload)))

	r.setMode(MODE_TX)
}

func (r *Radio) intrReceive() {
	if irq := r.readReg(REG_IRQFLAGS); irq&IRQ_RXDONE == 0 {
		// spurious interrupt?
		r.log("RX interrupt but no packet received")
		return
	}

	// Grab the payload
	len := r.readReg(REG_RXBYTES)
	ptr := r.readReg(REG_FIFORXCURR)
	r.writeReg(REG_FIFOPTR, ptr)
	var wBuf, rBuf [257]byte
	wBuf[0] = REG_FIFO
	r.spi.Tx(wBuf[:len+1], rBuf[:len+1])

	// Grab SNR, RSSI and FEI
	snr := int(r.readReg(REG_PKTSNR)) / 4
	rssi := int(uint32(int(r.readReg(REG_PKTRSSI))))
	rssi = -164 + rssi + rssi>>4
	if snr < 0 {
		rssi += snr
	}
	//fei := r.readReg24(REG_FEI)

	// Push packet into channel.
	pkt := RxPacket{Payload: rBuf[1 : len+1], Snr: snr, Rssi: rssi}
	select {
	case r.rxChan <- &pkt:
	default:
		r.log("rxChan full")
	}
}

// logRegs is a debug helper function to print almost all the sx1276's registers.
func (r *Radio) logRegs() {
	var buf, regs [0x50]byte
	buf[0] = 1
	r.spi.Tx(buf[:], regs[:])
	regs[0] = 0 // no real data there
	r.log("     0  1  2  3  4  5  6  7  8  9  A  B  C  D  E  F")
	for i := 0; i < len(regs); i += 16 {
		line := fmt.Sprintf("%02x:", i)
		for j := 0; j < 16 && i+j < len(regs); j++ {
			line += fmt.Sprintf(" %02x", regs[i+j])
		}
		r.log(line)
	}
}

// writeReg writes one or multiple registers starting at addr, the sx1276 auto-increments (except
// for the FIFO register where that wouldn't be desirable).
func (r *Radio) writeReg(addr byte, data ...byte) {
	wBuf := make([]byte, len(data)+1)
	rBuf := make([]byte, len(data)+1)
	wBuf[0] = addr | 0x80
	copy(wBuf[1:], data)
	r.spi.Tx(wBuf[:], rBuf[:])
}

// readReg reads one register and returns its value.
func (r *Radio) readReg(addr byte) byte {
	var buf [2]byte
	r.spi.Tx([]byte{addr & 0x7f, 0}, buf[:])
	return buf[1]
}

// readReg16 reads one 16-bit register and returns its value.
func (r *Radio) readReg16(addr byte) uint16 {
	var buf [3]byte
	r.spi.Tx([]byte{addr & 0x7f, 0, 0}, buf[:])
	return (uint16(buf[1]) << 8) | uint16(buf[2])
}

// readReg24 reads one 24-bit register and returns its value.
func (r *Radio) readReg24(addr byte) uint32 {
	var buf [4]byte
	r.spi.Tx([]byte{addr & 0x7f, 0, 0, 0}, buf[:])
	return (uint32(buf[1]) << 16) | (uint32(buf[2]) << 8) | uint32(buf[3])
}
