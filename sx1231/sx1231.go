// Copyright 2016 by Thorsten von Eicken, see LICENSE file

// The SX1231 package interfaces with a HopeRF RFM69 radio connected to an SPI bus.
//
// The RFM69 modules use a Semtech SX1231 or SX1231H radio chip and this
// package should work fine with other radio modules using the same chip. The only real
// difference will be the power output section where different modules use different output stage
// configurations.
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
// This driver does not do a number of things that other sx1231 drivers tend to do with the
// goal of leaving these tasks to higher-level drivers. This driver does not use the address
// filtering capability: it recevies all packets because that's simpler and the few extra interrupts
// should not matter to a system that can run Golang. It also accepts packets that have a CRC error
// and simply flags the error. It does not constrain the sync bytes, the frequency, or the data
// rates.
//
// The main limitations of this driver are that it operates the sx1231 in FSK variable-length packet
// mode and limits the packet size to the 66 bytes that fit into the FIFO, meaning that the payloads
// pushed into the TX channel must be 65 bytes or less, leaving one byte for the required packet
// length.
//
// The methods on the Radio object are not concurrency safe. Since they all deal with configuration
// this should not pose difficulties. The Error function may be called from multiple goroutines
// and obviously the TX and RX channels work well with concurrency.
package sx1231

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

// Radio represents a Semtech SX1231 radio as used in HopeRF's RFM69 modules.
type Radio struct {
	TxChan chan<- []byte    // channel to transmit packets
	RxChan <-chan *RxPacket // channel for received packets
	// configuration
	spi     spi.ConnCloser // SPI device to access the radio
	intrPin gpio.PinIn     // interrupt pin for RX and TX interrupts
	intrCnt int            // count interrupts
	sync    []byte         // sync bytes
	freq    uint32         // center frequency
	rate    uint32         // bit rate from table
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
	Sync   []byte    // RF sync bytes
	Freq   uint32    // frequency in Hz, Khz, or Mhz
	Rate   uint32    // data bitrate in bits per second, must exist in Rates table
	Logger LogPrintf // function to use for logging
}

// Rate describes the SX1231 configuration to achieve a specific bit rate.
type Rate struct {
	Fdev    int  // TX frequency deviation in Hz
	Shaping byte // 0:none, 1:gaussian BT=1, 2:gaussian BT=0.5, 3:gaussian BT=0.3
	RxBw    byte // value for rxBw register (0x19)
	AfcBw   byte // value for afcBw register (0x1A)
}

// Rates is the table of supported bit rates and their corresponding register settings. The map
// key is the bit rate in bits per second. In order to operate at a new bit rate the table can be
// extended by the client.
var Rates = map[uint32]Rate{
	49230: {45000, 0, 0x4A, 0x42}, // used by jeelabs driver
	50000: {45000, 0, 0x4A, 0x42}, // nice round number
}

// TODO: check whether the following is a better setting for 50kbps:
// freescale app note http://cache.nxp.com/files/rf_if/doc/app_note/AN4983.pdf?fpsp=1&WT_TYPE=Application%20Notes&WT_VENDOR=FREESCALE&WT_FILE_FORMAT=pdf&WT_ASSET=Documentation&fileExt=.pdf
// uses: 50kbps, 25khz Fdev, 1.0 gaussian filter, 156.24khz RxBW&AfcBW, 3416Hz LO offset, 497Hz DCC

// RxPacket is a received packet with stats.
type RxPacket struct {
	Payload []byte // payload, from address to last data byte, excluding length & crc
	CrcOK   bool   // whether received CRC is OK
	Crc     uint16 // received CRC
	Rssi    int    // rssi value for current packet
	Fei     int    // frequency error for current packet
}

// New initializes an sx1231 Radio given an spi.Conn and an interrupt pin, and places the radio
// in receive mode.
//
// The SPI bus must be set to 10Mhz max and mode 0.
//
// The RF sync bytes used are specified using the sync array, the frequency is specified
// in Hz, Khz, or Mhz, and the data bitrate is specified in bits per second and must match one
// of the rates in the Rates table.
//
// To transmit, push packet payloads into the returned txChan.
// Received packets will be sent on the returned rxChan, which has a small amount of
// buffering. The rxChan will be closed if a persistent error occurs when
// communicating with the device, use the Error() function to retrieve the error.
func New(dev spi.ConnCloser, intr gpio.PinIn, opts RadioOpts) (*Radio, error) {
	r := &Radio{
		spi: dev, intrPin: intr,
		mode: 255,
		err:  fmt.Errorf("sx1231 is not initialized"),
		log:  func(format string, v ...interface{}) {},
	}
	if opts.Logger != nil {
		r.log = opts.Logger
	}

	// Set SPI parameters.
	if err := dev.Speed(4 * 1000 * 1000); err != nil {
		return nil, fmt.Errorf("sx1231: cannot set speed, %v", err)
	}
	if err := dev.Configure(spi.Mode0, 8); err != nil {
		return nil, fmt.Errorf("sx1231: cannot set mode, %v", err)
	}

	// Try to synchronize communication with the sx1231.
	sync := func(pattern byte) error {
		for n := 10; n > 0; n-- {
			// Doing write transactions explicitly to get OS errors.
			r.writeReg(REG_SYNCVALUE1, pattern)
			if err := dev.Tx([]byte{REG_SYNCVALUE1 | 0x80, pattern}, []byte{0, 0}); err != nil {
				return fmt.Errorf("sx1231: %s", err)
			}
			// Read same thing back, we hope...
			v := r.readReg(REG_SYNCVALUE1)
			if v == pattern {
				return nil
			}
		}
		return errors.New("sx1231: cannot sync with chip")
	}
	if err := sync(0xaa); err != nil {
		return nil, err
	}
	if err := sync(0x55); err != nil {
		return nil, err
	}

	r.setMode(MODE_SLEEP)

	//embd.SetDirection("CSID1", embd.Out) // extra gpio used for debugging
	//embd.DigitalWrite("CSID1", 1)
	//embd.DigitalWrite("CSID1", 0)

	// Detect chip version.
	r.log("SX1231/SX1231 version %#x", r.readReg(REG_VERSION))

	// Write the configuration into the registers.
	for i := 0; i < len(configRegs)-1; i += 2 {
		r.writeReg(configRegs[i], configRegs[i+1])
	}

	// Configure the bit rate and frequency.
	r.SetRate(opts.Rate)
	r.SetFrequency(opts.Freq)

	// Configure the sync bytes.
	if len(opts.Sync) < 1 || len(opts.Sync) > 8 {
		return nil, fmt.Errorf("sx1231: invalid number of sync bytes: %d, must be 1..8",
			len(r.sync))
	}
	r.sync = opts.Sync
	wBuf := make([]byte, len(r.sync)+2)
	rBuf := make([]byte, len(r.sync)+2)
	wBuf[0] = REG_SYNCCONFIG | 0x80
	wBuf[1] = byte(0x80 + ((len(r.sync) - 1) << 3))
	copy(wBuf[2:], r.sync)
	r.spi.Tx(wBuf, rBuf)

	// Allocate channels for packets, give them some buffer but the reality is that
	// packets don't come in that fast either...
	r.rxChan = make(chan *RxPacket, rxChanCap)
	r.txChan = make(chan []byte, txChanCap)
	r.RxChan = r.rxChan
	r.TxChan = r.txChan

	// Initialize interrupt pin.
	if err := r.intrPin.In(gpio.Float, gpio.RisingEdge); err != nil {
		return nil, fmt.Errorf("sx1231: error initializing interrupt pin: %s", err)
	}

	// Test the interrupt function by configuring the radio such that it generates an interrupt
	// and then call WaitForEdge. Start by verifying that we don't have any pending interrupt.
	for r.intrPin.WaitForEdge(0) {
		r.log("Interrupt test shows an incorrect pending interrupt")
	}
	// Make the radio produce an interrupt.
	r.log("Interrupt pin is %v", r.intrPin.Read())
	r.setMode(MODE_FS)
	r.writeReg(REG_DIOMAPPING1, DIO_MAPPING+0xC0)
	if !r.intrPin.WaitForEdge(time.Second) {
		return nil, fmt.Errorf("sx1231: interrupts from radio do not work, try unexporting gpio%d", r.intrPin.Number())
	}
	r.writeReg(REG_DIOMAPPING1, DIO_MAPPING)
	for r.intrPin.WaitForEdge(0) {
	}

	// log register contents
	r.logRegs()

	// Finally turn on the receiver.
	go r.worker()
	r.err = nil // can get an interrupt anytime now...
	r.setMode(MODE_RECEIVE)

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
	// this is still 4 ppm, i.e. well below the radio's 32 MHz crystal accuracy
	// 868.0 MHz = 0xD90000, 868.3 MHz = 0xD91300, 915.0 MHz = 0xE4C000
	frf := (freq << 2) / (32000000 >> 11)
	//log.Printf("SetFreq: %d %d %02x %02x %02x", freq, uint32(frf<<6),
	//	byte(frf>>10), byte(frf>>2), byte(frf<<6))
	r.writeReg(REG_FRFMSB, byte(frf>>10), byte(frf>>2), byte(frf<<6))
}

// SetRate sets the bit rate according to the Rates table. The rate requested must use one of
// the values from the Rates table. If it is not, nothing is changed.
func (r *Radio) SetRate(rate uint32) {
	params, found := Rates[rate]
	if !found {
		return
	}

	// program bit rate, assume a 32Mhz osc
	var rateVal uint32 = (32000000 + rate/2) / rate
	r.writeReg(REG_BITRATEMSB, byte(rateVal>>8), byte(rateVal&0xff))
	// program frequency deviation
	var fStep float64 = 32000000.0 / 524288 // 32Mhz osc / 2^19 = 61.03515625 Hz
	fdevVal := uint32((float64(params.Fdev) + fStep/2) / fStep)
	r.writeReg(REG_FDEVMSB, byte(fdevVal>>8), byte(fdevVal&0xFF))
	// program data modulation register
	r.writeReg(REG_DATAMODUL, params.Shaping&0x3)
	// program RX bandwidth and AFC bandwidth
	r.writeReg(REG_RXBW, params.RxBw, params.AfcBw)
	// set AFC mode
	r.writeReg(REG_AFCCTRL, 0x20)
}

// SetPower configures the radio for the specified output power (TODO: should be in dBm).
func (r *Radio) SetPower(v byte) {
	if v > 0x1F {
		v = 0x1F
	}
	r.log("SetPower %ddBm", -18+int(v))
	r.writeReg(0x11, 0x80+v)
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

// setMode changes the radio's operating mode and changes the interrupt cause (if necessary), and
// then waits for the new mode to be reached.
func (r *Radio) setMode(mode byte) {
	mode = mode & 0x1c

	// If we're in the right mode then don't do anything.
	if r.mode == mode {
		return
	}

	// Set the interrupt mode if necessary.
	switch mode {
	case MODE_TRANSMIT:
		r.writeReg(REG_DIOMAPPING1, DIO_MAPPING+DIO_PKTSENT)
	case MODE_RECEIVE:
		r.writeReg(REG_DIOMAPPING1, DIO_MAPPING+DIO_SYNC)
	default:
		// Mode used when switching, make sure we don't get an interupt.
		r.writeReg(REG_DIOMAPPING1, DIO_MAPPING)
	}

	// Set the new mode.
	r.writeReg(REG_OPMODE, mode)

	// Busy-wait 'til the new mode is reached.
	for start := time.Now(); time.Since(start) < 100*time.Millisecond; {
		if val := r.readReg(REG_IRQFLAGS1); val&IRQ1_MODEREADY != 0 {
			r.mode = mode
			if mode == MODE_RECEIVE {
				// uncomment the next line to get the interrupt as soon as RSSI
				// comes on, useful for troubleshooting
				//r.writeReg(REG_DIOMAPPING1, DIO_MAPPING+DIO_RSSI)
			}
			return
		}
	}
	r.err = errors.New("sx1231: timeout switching modes")
}

// receiving checks whether a reception is currently in progress. It uses the sync match flag as
// earliest indication that something is coming in that is not noise. It also protects from a
// packet sitting in RX that hasn't been picked-up yet by the interrupt handler.
func (r *Radio) receiving() bool {
	// Can't be receiving if we're not in the right mode...
	if r.mode != MODE_RECEIVE {
		return false
	}
	irq1 := r.readReg(REG_IRQFLAGS1)
	irq2 := r.readReg(REG_IRQFLAGS2)
	//log.Printf("Rcv? %#02x %#02x", irq1, irq2)
	return irq1&IRQ1_SYNCMATCH != 0 || irq2&IRQ2_PAYLOADREADY != 0
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
				//r.log("interrupt")
				intrChan <- struct{}{}
			} else if r.intrPin.Read() == gpio.High {
				r.log("Interrupt was delayed!")
				intrChan <- struct{}{}
			} else {
				select {
				case <-intrStop:
					r.log("sx1231: rx interrupt goroutine exiting")
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
			//embd.DigitalWrite("CSID1", 1)
			r.intrCnt++
			// What this interrupt is about depends on the current mode.
			switch r.mode {
			case MODE_RECEIVE:
				r.intrReceive()
			case MODE_TRANSMIT:
				r.intrTransmit()
			default:
				r.log("Spurious interrupt in mode=%x", r.mode)
			}
			//embd.DigitalWrite("CSID1", 0)

		// Something to transmit.
		case payload := <-r.txChan:
			if r.receiving() {
				r.intrReceive()
			}
			if r.err == nil {
				r.send(payload)
			}
		}
	}
	r.log("sx1231: rx goroutine exiting, %s", r.err)
	// Signal to clients that something is amiss.
	close(r.rxChan)
	close(intrStop)
	r.intrPin.In(gpio.Float, gpio.NoEdge) // causes interrupt goroutine to exit
	r.spi.Close()
}

// send switches the radio's mode and starts transmitting a packet.
func (r *Radio) send(payload []byte) {
	// limit the payload to valid lengths
	switch {
	case len(payload) > 65:
		payload = payload[:65]
	case len(payload) == 0:
		return
	}
	r.setMode(MODE_FS)
	//r.writeReg(0x2D, 0x01) // set preamble to 1 (too short)
	//r.writeReg(0x2F, 0x00) // set wrong sync value

	// push the message into the FIFO.
	buf := make([]byte, len(payload)+1)
	buf[0] = byte(len(payload) + 1)
	copy(buf[1:], payload)
	r.writeReg(REG_FIFO|0x80, buf...)
	r.setMode(MODE_TRANSMIT)
	/*
		for i := 30; i > 0; i-- {
			val, err := r.readReg(REG_IRQFLAGS2)
			if err != nil {
				return err
			}
			m, _ := r.readReg(REG_OPMODE)
			v2, _ := r.readReg(REG_IRQFLAGS1)
			log.Printf("mode: %#02x flags: %#02x %#02x", m, v2, val)
			if val&IRQ2_PACKETSENT != 0 {
				return nil
			}
		}
		log.Printf("tx timeout")
	*/
}

// readInfo reads various reception stats about the arriving/arrived packet and populates
// an RxInfo struct with it.
func (r *Radio) readInfo() *rxInfo {
	// Collect rxinfo, start with rssi.
	rxInfo := &rxInfo{}
	//cfg, _ := r.readReg(REG_RSSICONFIG)
	//if cfg&0x02 == 0 {
	//rxInfo.rssi = 0 // RSSI not ready
	//} else {
	rssi := r.readReg(REG_RSSIVALUE)
	rxInfo.rssi = 0 - int(rssi)/2
	//}
	// Low noise amp gain.
	lna := r.readReg(REG_LNAVALUE)
	rxInfo.lna = int((lna >> 3) & 0x7)
	// Auto freq correction applied, caution: signed value.
	f := int(int16(r.readReg16(REG_AFCMSB)))
	rxInfo.afc = (f * (32000000 >> 13)) >> 6
	// Freq error detected, caution: signed value.
	f = int(int16(r.readReg16(REG_FEIMSB)))
	rxInfo.fei = (f * (32000000 >> 13)) >> 6
	//fmt.Printf("\nrxinfo: %+v", *rxInfo)
	return rxInfo
}

// intrTransmit handles an interrupt after transmitting.
func (r *Radio) intrTransmit() {
	r.log("TX interrupt")
	// Double-check that the packet got transmitted.
	if irq2 := r.readReg(REG_IRQFLAGS2); irq2&IRQ2_PACKETSENT != 0 {
		// awesome, packet sent, now receive.
		r.setMode(MODE_FS)
		r.setMode(MODE_RECEIVE)
	} // Else hope for another interrupt?
}

func (r *Radio) intrReceive() {
	// Quickly capture the RX stats.
	//i0 := r.readInfo()
	//r.log("RX Interrupt")

	/*defer func() {
		r.setMode(MODE_STANDBY)
		r.setMode(MODE_RECEIVE)
	}()*/

	// Assume we get an interrupt before the packet is received.
	t0 := time.Now()
	var crcOK bool
	for {
		// See whether we have a full packet.
		irq2 := r.readReg(REG_IRQFLAGS2)
		if irq2&IRQ2_PAYLOADREADY != 0 {
			crcOK = irq2&IRQ2_CRCOK != 0
			break
		}
		// Bail out if we're not actually receiving a packet.
		irq1 := r.readReg(REG_IRQFLAGS1)
		if irq1&(IRQ1_RXREADY|IRQ1_RSSI|IRQ1_TIMEOUT) != IRQ1_RXREADY|IRQ1_RSSI {
			//r.log("... not receiving? irq1=%#02x irq2=%02x", irq1, irq2)
			return
		}
		// Timeout so we don't get stuck here.
		if time.Since(t0).Seconds() > 100.0*8/50000 { // 100 bytes @50khz
			dbgPush("   timeout")
			return
		}
	}

	// get RSSI and Freq error for packet.
	rssi := 0 - int(r.readReg(REG_RSSIVALUE))/2
	// freq error detected, caution: signed value.
	f := int(int16(r.readReg16(REG_FEIMSB)))
	fei := (f * (32000000 >> 13)) >> 6

	//i := r.readInfo()
	//dbgPush(fmt.Sprintf("%+v", *i))

	// Got packet, read it by fetching the entire FIFO, should be faster than first
	// looking at the length.
	var wBuf, rBuf [67]byte
	wBuf[0] = REG_FIFO
	r.spi.Tx(wBuf[:], rBuf[:])

	// Push packet into channel.
	l := rBuf[1]
	switch {
	case l > 65:
		l = 65 // or error?
	case l < 1:
		l = 1 // or error
	}
	pkt := RxPacket{Payload: rBuf[2 : 2+l], CrcOK: crcOK, Rssi: rssi, Fei: fei}
	select {
	case r.rxChan <- &pkt:
	default:
		r.log("rxChan full")
	}
}

// logRegs is a debug helper function to print almost all the sx1231's registers.
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

// writeReg writes one or multiple registers starting at addr, the sx1231 auto-increments (except
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
