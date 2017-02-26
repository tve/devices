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
//
// Notes about the sx1231
//
// There are some 20 settings that all interact in undocumented ways, so
// getting to a robust driver is tricky. The worst is that on the RX side, once RSSI threshold is
// crossed the radio is locked at some frequency due to AFC and awaits a sync match. If that doesn't
// happen the receiver is effectively useless until it's reset. This means one either has to get an
// interrupt on RSSI and apply a timeout after which one restarts RX, or one has to configure the
// radio's RX timeout and use a second interrupt on that.
//
// If AFC is disabled, then the chip performs no FEI measurement, this makes it impossible to
// automatically tune the carrier frequency. If AFC is enabled and the AFC low-beta offset is also
// enabled, then the FEI measurement seems bogus and the reported AFC value includes the low-beta
// offset.
package sx1231

import (
	"errors"
	"fmt"
	"sync"
	"time"

	//"github.com/kidoman/embd"
	"github.com/tve/devices"
)

const rxChanCap = 4 // queue up to 4 received packets before dropping
const txChanCap = 4 // queue up to 4 packets to tx before blocking

// Radio represents a Semtech SX1231 radio as used in HopeRF's RFM69 modules.
type Radio struct {
	TxChan chan<- []byte    // channel to transmit packets
	RxChan <-chan *RxPacket // channel for received packets
	// configuration
	spi     devices.SPI  // SPI device to access the radio
	intrPin devices.GPIO // interrupt pin for RX and TX interrupts
	intrCnt int          // count interrupts
	sync    []byte       // sync bytes
	freq    uint32       // center frequency
	rate    uint32       // bit rate from table
	paBoost bool         // true: use PA1+PA2 power amp, else PA0
	power   byte         // output power in dBm
	// state
	sync.Mutex                // guard concurrent access to the radio
	mode       byte           // current operation mode
	rxTimeout  uint32         // RX timeout counter to tune rssi threshold
	err        error          // persistent error
	rxChan     chan *RxPacket // channel to push recevied packets into
	txChan     chan []byte    // channel for packets to be transmitted
	log        LogPrintf      // function to use for logging
}

// RadioOpts contains options used when initilizing a Radio.
type RadioOpts struct {
	Sync    []byte    // RF sync bytes
	Freq    uint32    // frequency in Hz, Khz, or Mhz
	Rate    uint32    // data bitrate in bits per second, must exist in Rates table
	PABoost bool      // true: use PA1+PA2, false: use PA0
	Logger  LogPrintf // function to use for logging
}

// Rate describes the SX1231 configuration to achieve a specific bit rate.
//
// The datasheet is somewhat confused and confusing about what Fdev and RxBw really mean.
// Fdev is defined as the deviation between the center freq and the modulated freq, while
// conventionally the frequency deviation fdev is the difference between the 0 and 1 freq's,
// thus the conventional fdev is Fdev*2.
//
// Similarly the RxBw is specified as the single-sided bandwidth while conventionally the
// signal or channel bandwidths are defined using the total bandwidths.
//
// Given that the sx1231 is a zero-if receiver it is recommended to configure a modulation index
// greater than 1.2, e.g. best approx 2. Modulation index is defined as fdev/bit-rate. This
// means that Fdev should be approx equal to bps. [Martyn, or are you targeting a modulation
// index of 4?]
//
// The signal bandwidth (20dB roll-off) can be approximated by fdev + bit-rate. Since RxBw
// is specified as the single-sided bandwidth it needs to be at least (fdev+bit-rate)/2. Or,
// in sx1231 config terms, Fdev + bitrate/2. If AFC is used, in order to accommodate a crystal
// offset between Tx and Rx of Fdelta the AFC bandwidth should be approx fdev + bit-rate +
// 2*Fdelta.
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
	49230: {45000, 0, 0x4A, 0x42},  // JeeLabs driver for rfm69 (RxBw=100, AfcBw=125)
	49231: {180000, 0, 0x49, 0x49}, // JeeLabs driver with rf12b compatibility
	49232: {45000, 0, 0x52, 0x4A},  // JeeLabs driver for rfm69 (RxBw=83, AfcBw=100)
	49233: {51660, 0, 0x52, 0x4A},  // JeeLabs driver for rfm69 (RxBw=83, AfcBw=100)
	50000: {90000, 0, 0x42, 0x42},  // nice round number
}

// TODO: check whether the following is a better setting for 50kbps:
// freescale app note http://cache.nxp.com/files/rf_if/doc/app_note/AN4983.pdf?fpsp=1&WT_TYPE=Application%20Notes&WT_VENDOR=FREESCALE&WT_FILE_FORMAT=pdf&WT_ASSET=Documentation&fileExt=.pdf
// uses: 50kbps, 25khz Fdev, 1.0 gaussian filter, 156.24khz RxBW&AfcBW, 3416Hz LO offset, 497Hz DCC

// RxPacket is a received packet with stats.
type RxPacket struct {
	Payload []byte    // payload, from address to last data byte, excluding length & crc
	Rssi    int       // rssi value for current packet
	Fei     int       // frequency error for current packet
	At      time.Time // time of rx interrupt
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
func New(dev devices.SPI, intr devices.GPIO, opts RadioOpts) (*Radio, error) {
	r := &Radio{
		spi: dev, intrPin: intr,
		mode:    255,
		paBoost: opts.PABoost,
		err:     fmt.Errorf("sx1231 is not initialized"),
		log:     func(format string, v ...interface{}) {},
	}
	if opts.Logger != nil {
		r.log = func(format string, v ...interface{}) {
			opts.Logger("sx1231: "+format, v...)
		}
	}

	// Set SPI parameters.
	if err := dev.Speed(4 * 1000 * 1000); err != nil {
		return nil, fmt.Errorf("sx1231: cannot set speed, %v", err)
	}
	if err := dev.Configure(devices.SPIMode0, 8); err != nil {
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
	r.setMode(MODE_STANDBY)

	//embd.SetDirection("CSID1", embd.Out) // extra gpio used for debugging
	//embd.DigitalWrite("CSID1", 1)
	//embd.DigitalWrite("CSID1", 0)

	// Detect chip version.
	r.log("SX1231/SX1231 version %#x", r.readReg(REG_VERSION))

	// Write the configuration into the registers.
	for i := 0; i < len(configRegs)-1; i += 2 {
		r.writeReg(configRegs[i], configRegs[i+1])
	}
	r.setMode(MODE_STANDBY)
	// Debug: read config regs back and make sure they are correct.
	//for i := 0; i < len(configRegs)-1; i += 2 {
	//	if r.readReg(configRegs[i]) != configRegs[i+1] {
	//		r.log("error writing config reg %#x: got %#x expected %#x",
	//			configRegs[i], r.readReg(configRegs[i]), configRegs[i+1])
	//	}
	//}

	// Configure the bit rate and frequency.
	r.SetRate(opts.Rate)
	r.SetFrequency(opts.Freq)
	r.SetPower(13)

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

	count := 0
repeat:
	// Initialize interrupt pin.
	if err := r.intrPin.In(devices.GpioRisingEdge); err != nil {
		return nil, fmt.Errorf("sx1231: error initializing interrupt pin: %s", err)
	}
	r.log("Interrupt pin is %v", r.intrPin.Read())

	// Test the interrupt function by configuring the radio such that it generates an interrupt
	// and then call WaitForEdge. Start by verifying that we don't have any pending interrupt.
	for r.intrPin.WaitForEdge(0) {
		r.log("Interrupt test shows an incorrect pending interrupt")
	}
	// Make the radio produce an interrupt.
	r.setMode(MODE_FS)
	r.writeReg(REG_DIOMAPPING1, DIO_MAPPING+0xC0)
	if !r.intrPin.WaitForEdge(100 * time.Millisecond) {
		if count == 0 {
			r.writeReg(REG_DIOMAPPING1, DIO_MAPPING)
			r.intrPin.Close()
			time.Sleep(100 * time.Millisecond)
			count++
			goto repeat
		}
		return nil, fmt.Errorf("sx1231: interrupts from radio do not work, try unexporting gpio%d", r.intrPin.Number())
	}
	r.writeReg(REG_DIOMAPPING1, DIO_MAPPING)
	// Flush any addt'l interrupts.
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
	r.log("SetFrequency: %dHz", freq)

	mode := r.mode
	r.setMode(MODE_STANDBY)
	// Frequency steps are in units of (32,000,000 >> 19) = 61.03515625 Hz
	// use multiples of 64 to avoid multi-precision arithmetic, i.e. 3906.25 Hz
	// due to this, the lower 6 bits of the calculated factor will always be 0
	// this is still 4 ppm, i.e. well below the radio's 32 MHz crystal accuracy
	// 868.0 MHz = 0xD90000, 868.3 MHz = 0xD91300, 915.0 MHz = 0xE4C000
	frf := (freq << 2) / (32000000 >> 11)
	//log.Printf("SetFreq: %d %d %02x %02x %02x", freq, uint32(frf<<6),
	//	byte(frf>>10), byte(frf>>2), byte(frf<<6))
	r.writeReg(REG_FRFMSB, byte(frf>>10), byte(frf>>2), byte(frf<<6))
	r.setMode(mode)
}

// SetRate sets the bit rate according to the Rates table. The rate requested must use one of
// the values from the Rates table. If it is not, nothing is changed.
func (r *Radio) SetRate(rate uint32) {
	params, found := Rates[rate]
	if !found {
		return
	}
	bw := func(v byte) int {
		return 32000000 / (int(16+(v&0x18>>1)) * (1 << ((v & 0x7) + 2)))
	}
	r.log("SetRate %dbps, Fdev:%dHz, RxBw:%dHz(%#x), AfcBw:%dHz(%#x) AFC off:%dHz", rate,
		params.Fdev, bw(params.RxBw), params.RxBw, bw(params.AfcBw), params.AfcBw,
		(params.Fdev/10/488)*488)

	r.rate = rate
	mode := r.mode
	r.setMode(MODE_STANDBY)
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
	// program AFC offset to be 10% of Fdev
	r.writeReg(REG_TESTAFC, byte(params.Fdev/10/488))
	if r.readReg(REG_AFCCTRL) != 0x00 {
		r.setMode(MODE_FS)            // required to write REG_AFCCTRL, undocumented
		r.writeReg(REG_AFCCTRL, 0x00) // 0->AFC, 20->AFC w/low-beta offset
	}
	r.setMode(mode)
}

// SetPower configures the radio for the specified output power (TODO: should be in dBm).
func (r *Radio) SetPower(dbm byte) {
	// Save current mode.
	mode := r.mode
	r.setMode(MODE_STANDBY)

	if r.paBoost {
		// rfm69H with external antenna switch.
		if dbm > 20 {
			dbm = 20
		}
		switch {
		case dbm <= 13:
			r.writeReg(REG_PALEVEL, 0x40+18+dbm) // PA1
		case dbm <= 17:
			r.writeReg(REG_PALEVEL, 0x60+14+dbm) // PA1+PA2
		default:
			r.writeReg(REG_PALEVEL, 0x60+11+dbm) // PA1+PA2+HIGH_POWER
		}
	} else {
		// rfm69 without external antenna switch.
		if dbm > 13 {
			dbm = 13
		}
		r.writeReg(REG_PALEVEL, 0x80+18+dbm) // PA0
	}
	// Technically the following two lines are for <=17dBm, but if we're set higher
	// then the registers get writte with the correct value each time the mode is switched
	// into Tx or Rx, so it's safe to do it unconditionally here.
	r.writeReg(REG_TESTPA1, 0x55)
	r.writeReg(REG_TESTPA2, 0x70)
	r.log("SetPower %ddBm", dbm)
	r.power = dbm

	// Restore operating mode.
	r.setMode(mode)
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
		if r.power > 17 {
			// To get >17dBm on the rfm69H some magic is required.
			r.writeReg(REG_TESTPA1, 0x5D)
			r.writeReg(REG_TESTPA2, 0x7C)
		}
		// Setting DIO_PKTSENT will not cause an intr.
		r.writeReg(REG_DIOMAPPING1, DIO_MAPPING+DIO_PKTSENT)
		// Set the new mode.
		r.writeReg(REG_OPMODE, mode)
	case MODE_RECEIVE:
		if r.power > 17 {
			// Turn off high power Tx stuff.
			r.writeReg(REG_TESTPA1, 0x5D)
			r.writeReg(REG_TESTPA2, 0x7C)
		}
		// We get here from MODE_FS and DIO_MAPPING, we need to switch to RX first and then
		// change DIO.
		r.writeReg(REG_OPMODE, mode)
		r.writeReg(REG_DIOMAPPING1, DIO_MAPPING+DIO_RSSI) // DIO_RSSI or DIO_SYNC
	default:
		// Mode used when switching, make sure we don't get an interupt.
		if r.mode == MODE_RECEIVE {
			r.writeReg(REG_DIOMAPPING1, DIO_MAPPING)
			r.writeReg(REG_OPMODE, mode)
		} else {
			r.writeReg(REG_OPMODE, mode)
			r.writeReg(REG_DIOMAPPING1, DIO_MAPPING)
		}
	}

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
	return irq1&IRQ1_SYNCMATCH != 0
}

// worker is an endless loop that processes interrupts for reception as well as packets
// enqueued for transmit.
func (r *Radio) worker() {
	// Interrupt goroutine converting WaitForEdge to a channel. We need a channel so we can
	// select between Rx and Tx.
	intrChan := make(chan struct{})
	intrStop := make(chan struct{})
	go func() {
		// Make sure we're not missing an initial edge due to a race condition.
		if r.intrPin.Read() == devices.GpioHigh {
			intrChan <- struct{}{}
		}
		// Start RSSI adjustment.
		r.rxTimeout = 0
		t0 := time.Now()
		// Loop over interrupts.
		for {
			if r.intrPin.WaitForEdge(time.Second) {
				//r.log("interrupt %d", r.intrPin.Read())
				if r.intrPin.Read() == 1 {
					intrChan <- struct{}{}
				}
			} else if r.intrPin.Read() == devices.GpioHigh {
				// Sometimes WaitForEdge times out yet the interrupt pin is
				// active, this means the driver or epoll failed us.
				// Need to understand this better.
				r.log("Interrupt was missed!")
				intrChan <- struct{}{}
			} else {
				select {
				case <-intrStop:
					r.log("rx interrupt goroutine exiting")
					return
				default:
				}
				// If we're in RX mode and the chip shows a timeout, then reset it.
				// This shouldn't happen but is here as a safety catch.
				if r.mode == MODE_RECEIVE && r.readReg(REG_IRQFLAGS1)&IRQ1_TIMEOUT != 0 {
					r.log("Rx restart")
					r.log("Mode: %#x, mapping: %#x, IRQ flags: %#x %#x",
						r.readReg(REG_OPMODE), r.readReg(REG_DIOMAPPING1),
						r.readReg(REG_IRQFLAGS1), r.readReg(REG_IRQFLAGS2))
					r.setMode(MODE_FS)
					r.setMode(MODE_RECEIVE)
				}
			}
			// Adjust RSSI threshold
			if dt := time.Since(t0); dt > 10*time.Second {
				timeoutPerSec := float64(r.rxTimeout) / dt.Seconds()
				switch {
				case timeoutPerSec > 10:
					r.writeReg(REG_RSSITHRES, r.readReg(REG_RSSITHRES)-1)
				case timeoutPerSec < 2.5:
					r.writeReg(REG_RSSITHRES, r.readReg(REG_RSSITHRES)+1)
				}
				r.log("RSSI threshold: %.2f timeout/sec, %.1fdBm **********",
					timeoutPerSec, -float64(r.readReg(REG_RSSITHRES))/2)
				r.rxTimeout = 0
				t0 = time.Now()
			}
		}
	}()

	// Main worker loop selecting between a receive opportunity and a transmit request.
	for r.err == nil {
		select {
		// interrupt
		case <-intrChan:
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

		// Something to transmit.
		case payload := <-r.txChan:
			if r.receiving() {
				// If we're receiving we switch over and complete that task.
				r.intrReceive()
			}
			if r.err == nil {
				r.send(payload)
			}
		}
	}
	r.log("rx goroutine exiting, %s", r.err)
	// Signal to clients that something is amiss.
	close(r.rxChan)
	close(intrStop)
	r.intrPin.In(devices.GpioNoEdge) // causes interrupt goroutine to exit
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
	buf[0] = byte(len(payload))
	copy(buf[1:], payload)
	r.writeReg(REG_FIFO|0x80, buf...)
	r.setMode(MODE_TRANSMIT)
}

// intrTransmit handles an interrupt after transmitting.
func (r *Radio) intrTransmit() {
	// Double-check that the packet got transmitted.
	if irq2 := r.readReg(REG_IRQFLAGS2); irq2&IRQ2_PACKETSENT == 0 {
		r.log("sx12331: TX done interrupt, but packet not transmitted? %#x", irq2)
	}
	// Now receive.
	r.setMode(MODE_RECEIVE)
}

// intrReceive handles a receive interrupt. The overall strategy is to get the interrupt either when
// the rssi threshold is crossed or a sync match is found. We then capture rssi and fei as soon as
// possible and wait for the end of the packet. If that doesn't come in we time out and restart rx,
// else we can pull the packet out of the fifo.
//
// Triggering the interrupt on RSSI threshold risks getting many spurious interrupts, but it also
// allows us to restart rx if the rx gets lured off-frequency by some noise due to AFC. Triggering
// on sync match reduces the interrupt freq but runs the AFC risk. Triggering only on
// packet-received means we don't really get a reliable rssi and fei.
//
// Notes: RSSI and AFC must be grabbed after sync match, else they're not yet stable.
// FEI right after sync match and after payload_ready are the same. RSSI are very similar (the
// transmission can fade in&out a bit). AFC includes the low-beta offset and thus isn't a good
// number to use.
func (r *Radio) intrReceive() {
	// Get timestamp and calculate timeout. At 50kbps a 2-byte ACK takes 2ms and a full 66 byte
	// packet takes 12.3ms.
	t0 := time.Now()
	tOut := t0.Add(time.Second * 80 * 8 / time.Duration(r.rate)) // time for 80 bytes

	// Helper function to empty FIFO. It's faster to read the whole thing than to first look at
	// the length.
	readFifo := func() []byte {
		var wBuf, rBuf [67]byte
		wBuf[0] = REG_FIFO
		r.Lock()
		r.spi.Tx(wBuf[:], rBuf[:])
		r.Unlock()
		return rBuf[1:]
	}

	// Loop until we have the full packet, or things go south. Grab RSSI & AFC/FEI only if we
	// can get them before the packet is fully received.
	var rssi, fei int
	for {
		// See whether we have a full packet.
		irq2 := r.readReg(REG_IRQFLAGS2)
		if irq2&IRQ2_PAYLOADREADY != 0 {
			if irq2&IRQ2_CRCOK == 0 {
				r.log("Rx bad CRC")
				readFifo()
				return
			}
			if rssi == 0 {
				r.log("Rx interrupt: packet was ready")
			}
			break
		}
		// Bail out if we're not actually receiving a packet. This could happen if the
		// receiver restarts.
		irq1 := r.readReg(REG_IRQFLAGS1)
		if irq1&(IRQ1_RXREADY|IRQ1_RSSI) != IRQ1_RXREADY|IRQ1_RSSI {
			r.log("... not receiving? irq1=%#02x irq2=%02x", irq1, irq2)
			return
		}
		// As soon as we have sync match, grab RSSI and FEI.
		if rssi == 0 && irq1&IRQ1_SYNCMATCH != 0 {
			// Get RSSI.
			rssi = 0 - int(r.readReg(REG_RSSIVALUE))/2
			// Get freq error detected, caution: signed 16-bit value.
			f := int(int16(r.readReg16(REG_AFCMSB)))
			fei = (f * (32000000 >> 13)) >> 6
		}
		// Timeout so we don't get stuck here.
		if time.Now().After(tOut) {
			//r.log("RX timeout! irq1=%#02x irq2=%02x, rssi=%ddBm afc=%dHz", irq1, irq2,
			//	0-int(r.readReg(REG_RSSIVALUE))/2,
			//	(int(int16(r.readReg16(REG_AFCMSB)))*(32000000>>13))>>6)
			r.rxTimeout++
			// Make sure the FIFO is empty (not sure this is necessary).
			if irq2&IRQ2_FIFONOTEMPTY != 0 {
				r.log("RX timeout! irq1=%#02x irq2=%02x, rssi=%ddBm afc=%dHz", irq1, irq2,
					0-int(r.readReg(REG_RSSIVALUE))/2,
					(int(int16(r.readReg16(REG_AFCMSB)))*(32000000>>13))>>6)
				buf := readFifo()
				r.log("FIFO: %+v", buf)
			}
			// Restart Rx.
			r.writeReg(REG_PKTCONFIG2, 0x16)
			return
		}
		time.Sleep(time.Millisecond)
	}

	/* get RSSI and FEI again and see whether they're any different
	rssi2 := 0 - int(r.readReg(REG_RSSIVALUE))/2
	f2 := int(int16(r.readReg16(REG_FEIMSB)))
	fei2 := (f2 * (32000000 >> 13)) >> 6
	if rssi > rssi2+4 || rssi < rssi2-4 {
		r.log("First rssi %ddBm != second rssi %ddBm", rssi, rssi2)
	}
	if fei != fei2 {
		r.log("First fei %dHz != second fei %dHz", fei, fei2)
	}*/

	// Got packet, read it.
	buf := readFifo()

	// Push packet into channel.
	l := buf[0]
	if l > 65 {
		r.log("Rx packet too long (%d)", l)
		return
	}
	pkt := RxPacket{Payload: buf[1 : 1+l], Rssi: rssi, Fei: fei}
	select {
	case r.rxChan <- &pkt:
	default:
		r.log("rxChan full")
	}
}

// logRegs is a debug helper function to print almost all the sx1231's registers.
func (r *Radio) logRegs() {
	var buf, regs [0x60]byte
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
	r.Lock()
	defer r.Unlock()
	wBuf := make([]byte, len(data)+1)
	rBuf := make([]byte, len(data)+1)
	wBuf[0] = addr | 0x80
	copy(wBuf[1:], data)
	r.spi.Tx(wBuf[:], rBuf[:])
}

// readReg reads one register and returns its value.
func (r *Radio) readReg(addr byte) byte {
	r.Lock()
	defer r.Unlock()
	var buf [2]byte
	r.spi.Tx([]byte{addr & 0x7f, 0}, buf[:])
	return buf[1]
}

// readReg16 reads one 16-bit register and returns its value.
func (r *Radio) readReg16(addr byte) uint16 {
	r.Lock()
	defer r.Unlock()
	var buf [3]byte
	r.spi.Tx([]byte{addr & 0x7f, 0, 0}, buf[:])
	return (uint16(buf[1]) << 8) | uint16(buf[2])
}
