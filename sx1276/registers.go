// Copyright 2016 by Thorsten von Eicken, see LICENSE file

package sx1276

const (
	REG_FIFO        = 0x00
	REG_OPMODE      = 0x01
	REG_FRFMSB      = 0x06
	REG_PACONFIG    = 0x09
	REG_OCP         = 0x0B
	REG_LNA         = 0x0C
	REG_FIFOPTR     = 0x0D
	REG_FIFOTXBASE  = 0x0E
	REG_FIFORXBASE  = 0x0F
	REG_FIFORXCURR  = 0x10
	REG_IRQMASK     = 0x11
	REG_IRQFLAGS    = 0x12
	REG_RXBYTES     = 0x13
	REG_MODEMSTAT   = 0x18
	REG_PKTSNR      = 0x19
	REG_PKTRSSI     = 0x1A
	REG_CURRSSI     = 0x1B
	REG_HOPCHAN     = 0x1C
	REG_MODEMCONF1  = 0x1D
	REG_MODEMCONF2  = 0x1E
	REG_SYMBTIMEOUT = 0x1F
	REG_PREAMBLE    = 0x21
	REG_PAYLENGTH   = 0x22
	REG_PAYMAX      = 0x23
	REG_FIFORXLAST  = 0x25
	REG_MODEMCONF3  = 0x26
	REG_PPMCORR     = 0x27
	REG_FEI         = 0x28
	REG_DETECTOPT   = 0x31
	REG_DETECTTHR   = 0x37
	REG_SYNC        = 0x39
	REG_DIOMAPPING1 = 0x40
	REG_DIOMAPPING2 = 0x41
	REG_VERSION     = 0x42
	REG_TCXO        = 0x4B
	REG_PADAC       = 0x4D
	REG_FORMERTEMP  = 0x5B
)

const (
	MODE_SLEEP = iota
	MODE_STANDBY
	MODE_FS_TX     // frequency synthesis TX
	MODE_TX        // TX
	MODE_FS_RX     // frequency synthesis RX
	MODE_RX_CONT   // RX continuous
	MODE_RX_SINGLE // RX single
	MODE_CAD       // channel activity detection
)

const (
	// IRQ mask and flags registers
	IRQ_RXTIMEOUT = 1 << 7
	IRQ_RXDONE    = 1 << 6
	IRQ_CRCERR    = 1 << 5
	IRQ_VALIDHDR  = 1 << 4
	IRQ_TXDONE    = 1 << 3
	IRQ_CADDONE   = 1 << 2
	IRQ_FHSCHG    = 1 << 1
	IRQ_CADDETECT = 1 << 0
)

// register values to initialize the chip, this array has pairs of <address, data>
var configRegs = []byte{
	0x01, 0x88, // OpMode = LoRA+LF+sleep
	0x01, 0x88, // OpMode = LoRA+LF+sleep
	0x0B, 0x32, // Over-current protection @150mA
	0x0C, 0x23, // max LNA gain
	0x0D, 0x00, // FIFO ptr = 0
	0x0E, 0x00, // FIFO TX base = 0
	0x0F, 0x00, // FIFO RX base = 0
	0x10, 0x00, // FIFO RX current = 0
	0x11, 0x12, // mask valid header and FHSS change interrupts
	0x1f, 0xff, // RX timeout at 255 bytes
	0x20, 0x00, 0x21, 0x0A, // preamble of 8
	0x23, 0xFF, // max payload of 255 bytes
	0x24, 0x00, // no freq hopping
	0x27, 0x00, // no ppm freq correction
	0x31, 0x03, // detection optimize for SF7-12
	0x33, 0x27, // no I/Q invert
	0x37, 0x0A, // detection threshold for SF7-12
	0x40, 0x00, // DIO mapping 1
	0x41, 0x00, // DIO mapping 2
	//0x61, 0x19, 0x62, 0x0C, 0x63, 0x4b, 0x64, 0xcc, // default LF AGC thresholds
	//0x70, 0xd0, // default PLL threshold
}

// register values to initialize the chip in FSK mode to sweep the spectrum for antenna
// tuning, for example.
var sweepRegs = []byte{
	0x01, 0x08, // FSK + LF + sleep
	0x02, 0x05, 0x03, 0x00, // 25kbps
	0x04, 0x01, 0x05, 0x97, // Fdev 25khz
	0x0A, 0x09, // no shaping, 40us PA ramp
	0x0B, 0x00, // disable OCP
	0x0C, 0x20, // max LNA gain
	0x0D, 0x9f, // enable AFC & AGC
	0x0E, 0x04, // smooth RSSI using 32 samples
	0x10, 170, // rssi thres: -85dB
	0x12, 0x0b, 0x13, 0x0b, // RX BW 50khz
	0x1a, 0x01, // auto-clear AFC
	0x1b, 0, 0x1c, 0, // reset AFC values
	0x1f, 0xAA, // turn on preamble detector
	0x20, 0, 0x21, 0, 0x22, 0, 0x23, 0, // disable RX timeouts
	0x24, 0x07, // disable clkout
	0x25, 0x00, 0x26, 0x06, // 6-byte preamble
	0x27, 0x91, // rx auto-restart, 2 sync bytes
	0x28, 0xC5, 0x29, 0x3A, // sync bytes
	0x30, 0x08, // fixed length, no whitening, no addr filtering
	0x31, 0x40, 0x32, 10, // packet mode, 10-byte packets
	0x35, 0xA0, // start TX as soon as first byte is pushed, fifo thresh:32
	0x44, 0x80, // enable fast hop
	0x4d, 0x14, // disable 20bDm boost
}
