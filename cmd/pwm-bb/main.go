// Copyright 2016 The Periph Authors. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/periph/conn/gpio"
	"github.com/google/periph/host"
)

func mainImpl() error {
	if len(os.Args) != 3 {
		return errors.New("specify GPIO pin to write to and the freq in Hz")
	}

	if _, err := host.Init(); err != nil {
		return err
	}

	p := gpio.ByName(os.Args[1])
	if p == nil {
		return errors.New("invalid GPIO pin number")
	}

	if err := p.Out(gpio.Low); err != nil {
		return err
	}
	for {
		p.Out(gpio.High)
		p.Out(gpio.Low)
		time.Sleep(1 * time.Microsecond)
	}

	return nil
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "gpio-write: %s.\n", err)
		os.Exit(1)
	}
}
