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

	"periph.io/x/periph/conn/gpio"
	"periph.io/x/periph/conn/spi"
)

// Radio represents a Semtech SX127x LoRA radio.
type Radio struct {
	// configuration
	spi     spi.Conn   // SPI device to access the radio
	intrPin gpio.PinIn // interrupt pin for RX and TX interrupts
	intrCnt int        // count interrupts
	sync    byte       // sync byte
	freq    uint32     // center frequency in Hz
	config  string     // entry in Configs table being used
	// state
	sync.Mutex           // guard concurrent access to the radio
	mode       byte      // current operation mode
	err        error     // persistent error
	log        LogPrintf // function to use for logging
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
	Conf1 byte   // ModemConfig1: bw, coding rate, implicit/expl header
	Conf2 byte   // ModemConfig2: sperading, mode, crc
	Conf3 byte   // ModemConfig3: low data rate opt, LNA gain
	Info  string // info for humans
}

// Configs is the table of supported configurations and their corresponding register settings.
// In order to operate at a new bit rate the table can be extended by the client.
// The names use bw: bandwidth in kHz, cr: coding rate 4/5..4/8, and sf: spreading factor.
var Configs = map[string]Config{
	// Configurations from radiohead library, the first one is fast for short range, the
	// second intermediate for medium range, and the last two slow for long range.
	"lora.bw500cr45sf7":  {0x92, 0x74, 0x04, "31250bps, 20B in   14ms"},
	"lora.bw125cr45sf7":  {0x72, 0x74, 0x04, " 7813bps, 20B in   57ms"},
	"lora.bw125cr48sf12": {0x78, 0xc4, 0x04, "  183bps, 20B in 1712ms"},
	"lora.bw31cr48sf9":   {0x48, 0x94, 0x04, "  275bps, 20B in  987ms"},
	// Configurations from LoRaWAN standard.
	"lorawan.bw125sf12": {0x72, 0xc4, 0x0C, "  250bps, 20B in 1319ms, -137dBm"},
	"lorawan.bw125sf11": {0x72, 0xb4, 0x0C, "  440bps, 20B in  660ms, -136dBm"},
	"lorawan.bw125sf10": {0x72, 0xa4, 0x04, "  980bps, 20B in  370ms, -134dBm"},
	"lorawan.bw125sf9":  {0x72, 0x94, 0x04, " 1760bps, 20B in  185ms, -131dBm"},
	"lorawan.bw125sf8":  {0x72, 0x84, 0x04, " 3125bps, 20B in  103ms, -128dBm"},
	"lorawan.bw125sf7":  {0x72, 0x74, 0x04, " 5470bps, 20B in   57ms, -125dBm"},
	"lorawan.bw250sf7":  {0x82, 0x74, 0x04, "11000bps, 20B in   28ms, -122dBm"},
}

// RxPacket is a received packet with stats.
type RxPacket struct {
	Payload []byte    // payload, excluding length & crc
	Snr     int       // signal-to-noise in dB for packet
	Rssi    int       // rssi in dB for packet
	Fei     int       // frequency error in Hz for packet
	Lna     int       // dB of LNA applied
	At      time.Time // time of recv interrupt
}

// Temporary is an interface implemented by errors that are temporary and thus worth retrying.
type Temporary interface {
	Temporary() bool
}

type busyError struct{ e string }

func (b busyError) Error() string   { return b.e }
func (b busyError) Temporary() bool { return true }

var debugPin gpio.PinOut

// New initializes an sx1276 Radio given an spi.Conn and an interrupt pin, and places the radio
// in receive mode.
//
// To transmit, push packet payloads into the returned txChan.
// Received packets will be sent on the returned rxChan, which has a small amount of
// buffering. The rxChan will be closed if a persistent error occurs when
// communicating with the device, use the Error() function to retrieve the error.
func New(port spi.Port, intr gpio.PinIn, opts RadioOpts) (*Radio, error) {
	r := &Radio{
		intrPin: intr,
		mode:    255,
		err:     fmt.Errorf("sx1276 is not initialized"),
		log:     func(format string, v ...interface{}) {},
	}
	if opts.Logger != nil {
		r.log = opts.Logger
	}

	// Set SPI parameters and get a connection.
	conn, err := port.DevParams(4*1000*1000, spi.Mode0, 8)
	if err != nil {
		return nil, fmt.Errorf("sx1276: cannot set device params: %v", err)
	}
	r.spi = conn

	// Try to synchronize communication with the sx1276.
	sync := func(pattern byte) error {
		for n := 10; n > 0; n-- {
			// Doing write transactions explicitly to get OS errors.
			r.writeReg(REG_SYNC, pattern)
			if err := conn.Tx([]byte{REG_SYNC | 0x80, pattern}, []byte{0, 0}); err != nil {
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

	// Try to get the chip out of any mode it may be stuck in...
	r.setMode(MODE_SLEEP)
	time.Sleep(10 * time.Millisecond)
	r.setMode(MODE_STANDBY)

	// Detect chip version.
	r.log("SX1276 version %#x", r.readReg(REG_VERSION))

	// Better be in standby mode...
	m := r.readReg(REG_OPMODE)
	if m != 0x88+MODE_STANDBY {
		return nil, fmt.Errorf("sx1276: can't put radio into standby mode: %#x", m)
	}

	// Write the configuration into the registers.
	for i := 0; i < len(configRegs)-1; i += 2 {
		r.writeReg(configRegs[i], configRegs[i+1])
	}

	// Configure the transmission parameters.
	r.SetConfig(opts.Config)
	r.SetFrequency(opts.Freq)
	r.SetPower(17)

	//r.sync = opts.Sync
	r.spi.Tx([]byte{REG_SYNC | 0x80, opts.Sync}, []byte{0, 0})

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
	if err := r.Transmit([]byte{0}); err != nil {
		return nil, fmt.Errorf("sx1276: cannot perform test transmit: %s", err)
	}
	if !r.intrPin.WaitForEdge(time.Second) {
		r.logRegs()
		v := r.intrPin.Read()
		r.log("Interrupt pin is %v", v)
		if v == gpio.High {
			return nil, fmt.Errorf("sx1276: interrupts from radio do not work, try unexporting gpio%d", r.intrPin.Number())
		} else {
			return nil, fmt.Errorf("sx1276: the radio is not generating an interrupt, odd...")
		}
	}
	r.writeReg(REG_IRQFLAGS, 0xff) // clear IRQ
	time.Sleep(10 * time.Millisecond)
	for r.intrPin.WaitForEdge(0) {
	}

	// log register contents
	r.logRegs()

	// Finally turn on the receiver.
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
	mode := r.mode
	r.setMode(MODE_STANDBY)
	r.writeReg(REG_FRFMSB, byte(frf>>10), byte(frf>>2), byte(frf<<6))
	r.log("SetFreq %dHz -> %#x %#x %#x", freq, byte(frf>>10), byte(frf>>2), byte(frf<<6))
	r.setMode(mode)
	r.freq = freq
}

// SetConfig sets the modem configuration using one of the entries in the Configs table.
// If the entry specified does not exist, nothing is changed.
func (r *Radio) SetConfig(config string) {
	conf, found := Configs[config]
	if !found {
		return
	}
	r.log("SetConfig %s", config)

	mode := r.mode
	r.setMode(MODE_STANDBY)
	r.writeReg(REG_MODEMCONF1, conf.Conf1&^1)        // Explicit header mode
	r.writeReg(REG_MODEMCONF2, conf.Conf2&0xf0|0x04) // TxSingle, CRC enable
	r.writeReg(REG_MODEMCONF3, conf.Conf3|0x04)      // enable LNA AGC
	r.setMode(mode)
	r.config = config
}

// bandwidth returns the current signal bandwidth in Hz
func (r *Radio) bandwidth() int {
	return []int{
		7800, 10400, 15600, 20800, 31250, 41700, 62500, 125000, 250000, 500000,
		0, 0, 0, 0, 0, 0, // invalid settings
	}[Configs[r.config].Conf1>>4]
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
	mode := r.mode
	r.setMode(MODE_STANDBY)
	if dBm > 17 {
		r.writeReg(REG_PADAC, 0x87) // turn 20dBm mode on, this offsets the PACONFIG by 3
		r.writeReg(REG_PACONFIG, 0xf0+dBm-5)
	} else {
		r.writeReg(REG_PACONFIG, 0xf0+dBm-2)
		r.writeReg(REG_PADAC, 0x84)
	}
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
	//r.log("mode w=%#x r=%#x", mode+0x88, r.readReg(REG_OPMODE))
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
	return st&0xA != 0 || irq&IRQ_RXDONE != 0
}

// worker is an endless loop that processes interrupts for reception as well as packets
// enqueued for transmit.
func (r *Radio) Receive() (*RxPacket, error) {
	r.Lock()
	defer r.Unlock()

	// Loop over interrupts & timeouts.
	// Make sure we're not missing an initial edge due to a race condition.
	intr := r.intrPin.Read() == gpio.High
	for {
		if !intr {
			r.Unlock()
			intr = r.intrPin.WaitForEdge(1 * time.Second)
			r.Lock()
		}

		if !intr && r.intrPin.Read() == gpio.High {
			// Sometimes WaitForEdge times out yet the interrupt pin is
			// active, this means the driver or epoll failed us.
			// Need to understand this better.
			r.log("Interrupt was missed!")
		}
		intr = false

		if r.intrPin.Read() == gpio.High {
			switch {
			case r.mode == MODE_RX_CONT:
				pkt, err := r.rx(time.Now())
				if pkt != nil || err != nil {
					return pkt, err
				}
			case r.mode == MODE_TX:
				r.setMode(MODE_RX_CONT)
			default:
				r.log("Spurious interrupt in mode=%x", r.mode)
			}
			r.writeReg(REG_IRQFLAGS, 0xff) // clear IRQ
		}
	}
}

// Transmit switches the radio's mode and starts transmitting a packet.
func (r *Radio) Transmit(payload []byte) error {
	r.Lock()
	defer r.Unlock()

	if r.receiving() {
		return busyError{"radio is busy"}
	}
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
	return nil
}

func (r *Radio) rx(at time.Time) (*RxPacket, error) {
	irq := r.readReg(REG_IRQFLAGS)
	switch {
	case irq&IRQ_CRCERR != 0:
		r.log("RX CRC error (%#x)", irq)
		return nil, nil
	case irq&IRQ_RXDONE == 0: // spurious interrupt?
		r.log("RX interrupt but no packet received (%#x)", irq)
		return nil, nil
	case irq != 0x40:
		r.log("RX OK??? (%#x)", irq)
	}
	if (r.readReg(REG_HOPCHAN) & 0x40) == 0 {
		r.log("RX packet without CRC")
		return nil, nil
	}

	// Grab the payload
	len := r.readReg(REG_RXBYTES)
	ptr := r.readReg(REG_FIFORXCURR)
	r.writeReg(REG_FIFOPTR, ptr)
	var wBuf, rBuf [257]byte
	wBuf[0] = REG_FIFO
	r.spi.Tx(wBuf[:len+1], rBuf[:len+1])

	// Grab SNR, RSSI and FEI
	snr := int(int8(r.readReg(REG_PKTSNR))) / 4
	rssi := int(r.readReg(REG_PKTRSSI))
	rssi = -164 + rssi + rssi>>4
	if snr < 0 {
		rssi += snr
	}
	// if rssi > 0 { // sometimes the value fetched is way off, sigh...
	// fmt.Printf("RSSI %d SNR %d reg %d\n", rssi, snr, r.readReg(REG_PKTRSSI))
	// }
	f1 := int32(r.readReg24(REG_FEI))
	f2 := int64((f1 << 12) >> 12)                  // sign-extend 20-bit value
	fei := int(f2 * int64(r.bandwidth()) / 953674) // 953674=32Mhz*500/2^24
	lna := int(r.readReg(REG_LNA) >> 5)

	// Construct RxPacket and return it.
	pkt := RxPacket{Payload: rBuf[1 : len+1], Snr: snr, Rssi: rssi, Fei: fei, Lna: lna, At: at}
	return &pkt, nil
}

// logRegs is a debug helper function to print almost all the sx1276's registers.
func (r *Radio) logRegs() {
	var buf, regs [0x71]byte
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
	r.log("reg24: %+v", buf[1:4])
	return (uint32(buf[1]) << 16) | (uint32(buf[2]) << 8) | uint32(buf[3])
}
