package devices

// stuff in here is a hack to be able to switch between embd and some other library...

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kidoman/embd"
)

type SPI interface {
	//fmt.Stringer
	//io.Writer
	Tx(w, r []byte) error
	Speed(hz int64) error
	Configure(mode int, bits int) error
	Close() error
}

const (
	SPIMode0 = 0x0 // CPOL=0, CPHA=0
	SPIMode1 = 0x1 // CPOL=0, CPHA=1
	SPIMode2 = 0x2 // CPOL=1, CPHA=0
	SPIMode3 = 0x3 // CPOL=1, CPHA=1
)

type GPIO interface {
	In(edge int) error
	Read() int
	WaitForEdge(timeout time.Duration) bool
	Out(level int)
	Number() int
}

const (
	GpioLow        = 0
	GpioHigh       = 1
	GpioNoEdge     = 0
	GpioRisingEdge = 1
)

//===== SPI shim for embd

func NewSPI() SPI {
	return &spi{embd.NewSPIBus(embd.SPIMode0, 0, 4, 8, 0)}
}

type spi struct {
	embd.SPIBus
}

func (s *spi) Tx(w, r []byte) error {
	copy(r, w)
	return s.TransferAndReceiveData(r)
}

func (s *spi) Speed(hz int64) error {
	if hz != 4000000 {
		return errors.New("SPI: sorry, only 4Mhz supported")
	}
	return nil
}

func (s *spi) Configure(mode int, bits int) error {
	if mode != SPIMode0 {
		return errors.New("SPI: sorry, only SPI mode 0 supported")
	}
	if bits != 8 {
		return errors.New("SPI: sorry, only 8-bit mode supported")
	}
	return nil
}

//===== GPIO shim for embd

func NewGPIO(name string) GPIO {
	g, err := embd.NewDigitalPin(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewDigitalPin: %s\n", err)
		return nil
	}
	return &gpio{p: g, dir: embd.In, edge: make(chan struct{}, 1)}
}

type gpio struct {
	p    embd.DigitalPin
	dir  embd.Direction
	edge chan struct{}
}

func (g *gpio) In(edge int) error {
	if err := g.p.SetDirection(embd.In); err != nil {
		return err
	}
	g.dir = embd.In
	if edge != GpioNoEdge {
		e := []embd.Edge{embd.EdgeNone, embd.EdgeRising, embd.EdgeFalling, embd.EdgeBoth}[edge]
		//fmt.Fprintf(os.Stderr, "Watching pin %d\n", g.p.N())
		return g.p.Watch(e, g.edgeCB)
	}
	return nil
}

func (g *gpio) Read() int {
	v, _ := g.p.Read()
	return v
}

func (g *gpio) WaitForEdge(timeout time.Duration) bool {
	to := time.After(timeout)
	select {
	case <-g.edge:
		return true
	case <-to:
		return false
	}
}

func (g *gpio) Out(level int) {
	if g.dir != embd.Out {
		g.p.SetDirection(embd.Out)
		g.dir = embd.In
	}
	g.p.Write(level)
}

func (g *gpio) Number() int {
	return g.p.N()
}

func (g *gpio) edgeCB(embd.DigitalPin) {
	//fmt.Fprintf(os.Stderr, "Intr %d\n", g.p.N())
	select {
	case g.edge <- struct{}{}:
	default:
	}
}
