// Copyright 2016 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/google/periph/conn/spi"
	"github.com/google/periph/host"
	"github.com/tve/devices/max31855"
)

func mainImpl() error {
	if len(os.Args) > 2 {
		return errors.New("Optionally specify the SPI bus name or number")
	}
	n := -1
	if len(os.Args) == 2 {
		var err error
		if n, err = strconv.Atoi(os.Args[1]); err != nil {
			return err
		}
	}

	if _, err := host.Init(); err != nil {
		return err
	}

	s, err := spi.New(n, 0)
	if err != nil {
		return err
	}

	d, err := max31855.New(s)
	if err != nil {
		return err
	}

	// Loop to collect multiple samples and choose the median value. This seems to be
	// necessary because every now and then the max31855 seems to return a bad value,
	// depends a lot on noise...
	// This loop iterates until it got 3 errors or 3 measurements. It sleeps 100ms
	// between readings to give the max31855 time to perform a fresh ADC.
	var temp, iTemp [3]float64
	var nTemp, nErr int
	for {
		eT, iT, err := d.Temperature()
		if err != nil {
			nErr++
			if nErr == 3 {
				return err
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		temp[nTemp] = eT.Float64()
		iTemp[nTemp] = iT.Float64()
		nTemp++
		if nTemp == 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Sort measurements so we can pick the median.
	sort.Float64s(temp[:])
	sort.Float64s(iTemp[:])

	fmt.Printf("Thermocouple: %.1f°C internal: %.2f°C\n", temp[1], iTemp[1])

	return nil
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "max31855: %s.\n", err)
		os.Exit(1)
	}
}
