// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/periph/conn/gpio"
	"github.com/google/periph/conn/spi"
	"github.com/google/periph/host"
	"github.com/tve/devices/spimux"
	"github.com/tve/devices/sx1276"
)

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	_, err := host.Init()
	panicIf(err)

	selPinName := ""
	selPin := gpio.ByName("CSID0")
	if selPin == nil {
		panic("Cannot open pin " + selPinName)
	}

	spiBus, err := spi.New(-1, 0)
	panicIf(err)

	_, spi1276 := spimux.New(spiBus, selPin)

	log.Printf("Initializing LoRA sweeper...")
	t0 := time.Now()
	sweep, err := sx1276.NewSweeper(spi1276, 434000000, log.Printf)
	panicIf(err)
	log.Printf("Ready (%.1fms)", time.Since(t0).Seconds()*1000)
	sweep.SetPower(15)

	if len(os.Args) > 1 && os.Args[1] == "tx" {
		tx(sweep)
	} else {
		rx(sweep)
	}
}

func sweep(s *sx1276.Sweeper) {
	s.SetPower(17)
	s.SetFrequency(433600000)
	s.StartTx()
	time.Sleep(120 * time.Second)
	for i := uint32(433000); i < 435000; i += 10 {
		s.SetFrequency(i)
		time.Sleep(10 * time.Second)
	}
	s.SetFrequency(433500000)
	time.Sleep(5 * time.Minute)
	s.StopTx()
}

func tx(s *sx1276.Sweeper) {
	if len(os.Args) != 3 {
		log.Printf("usage: sx1276-sweep tx host")
		os.Exit(1)
	}

	// Open connection to receiver.
	host := os.Args[2]
	addr, err := net.ResolveTCPAddr("tcp4", host+":5665")
	panicIf(err)
	conn, err := net.DialTCP("tcp4", nil, addr)
	panicIf(err)

	// Send starting frequency and step. Then wait for ACK.
	endFreq := uint32(470000)
	freq := uint32(410000)
	step := uint32(10)
	s.SetFrequency(freq)
	s.LogRegs()
	_, err = conn.Write([]byte(fmt.Sprintf("%d %d\n", freq, step)))
	i1, i2, eof := read2Int(conn)
	if eof {
		log.Printf("EOF on socket")
		return
	}
	if i1 != 0 || i2 != 0 {
		log.Printf("Bad handshake")
		return
	}

	for freq <= endFreq {
		// Set frequency and send packet.
		//s.SetFrequency(freq)
		time.Sleep(1 * time.Millisecond)
		s.TxPacket()

		// Read response.
		fei, rssi, eof := read2Int(conn)
		if eof {
			log.Printf("EOF on socket")
			return
		}

		log.Printf("Freq: %9d %d: %4d", freq, fei, rssi)
		fmt.Printf("%9d, %4d\n", freq, rssi)
		panicIf(s.Error())

		freq += step
	}
	time.Sleep(100 * time.Millisecond)
	log.Printf("Bye...")

}

func rx(s *sx1276.Sweeper) {
	// Open TCP socket to receive frequency, step pairs.
	addr, err := net.ResolveTCPAddr("tcp4", "0.0.0.0:5665")
	panicIf(err)
	listen, err := net.ListenTCP("tcp4", addr)
	panicIf(err)
	conn, err := listen.Accept()
	panicIf(err)

	// Get first freq/step pair to get going and provide empty ACK.
	f, step, eof := read2Int(conn)
	if eof {
		return
	}
	freq := uint32(f)
	s.SetFrequency(freq)
	_, err = conn.Write([]byte("0 0\n")) // ACK
	panicIf(err)

	for {
		// RX packet.
		fei, rssi := s.RxPacket()
		_, err = conn.Write([]byte(fmt.Sprintf("%d %d\n", fei, rssi)))
		log.Printf("Freq: %9d/%9d: %4d", uint32(freq), fei, rssi)
		panicIf(s.Error())

		// Adjust freq.
		freq += uint32(step)
		//s.SetFrequency(uint32(freq))
		//time.Sleep(time.Millisecond)
	}

}

func read2Int(conn net.Conn) (int, int, bool) {
	var buf [128]byte
	n, err := conn.Read(buf[:])
	if err == io.EOF || err == nil && n == 0 {
		log.Printf("EOF on socket")
		return 0, 0, true
	}
	panicIf(err)
	b := buf[:n]
	if b[n-1] == '\n' {
		b = b[:n-1]
	}
	str := strings.Split(string(b), " ")
	i1, err := strconv.Atoi(str[0])
	i2, err := strconv.Atoi(str[1])
	panicIf(err)
	return i1, i2, false
}
