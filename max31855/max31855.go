// Copyright 2016 by Thorsten von Eicken, see LICENSE file

// The max31855 package interfaces with the Maxim Integrated MAX31855 thermocouple
// to digital converter chip.
//
// The MAX31855 chip contains an analog-to-digital converter that is designed to read the
// low voltages produced by thermocouples and convert them to degrees centigrate which can
// be read out using a read-only SPI interface. The MAX31855 comes in a number of variants
// for the different types of thermocouples (max31855K for K-type, max31855J for J-type, etc).
//
// The max31855 itself contains a temperature sensor, which is required to perform the temperature
// conversion and it is important to keep the junction between the thermocouple wires and the copper
// traces leading to the max31855 and the max31855 itself at the same temperature.
//
// The max31855 measures the thermocouple temperature to a resolution of 0.25°C and its internal
// temperature to 0.0625°C. The absolute accuracy, however, is +/-2°C for K-type thermocouples in
// the -200°C..700°C range as well as for the internal temperature sensor.
//
//
// Datasheet: https://datasheets.maximintegrated.com/en/ds/MAX31855.pdf
package max31855

import (
	"fmt"

	"github.com/google/periph/conn/spi"
	"github.com/google/periph/devices"
)

// Dev represents a MAX31855 device.
type Dev struct {
	spi spi.Conn
}

// New returns a
func New(s spi.Conn) (*Dev, error) {
	if err := s.Configure(spi.Mode0, 8); err != nil {
		return nil, fmt.Errorf("max31855: configure error: %v", err)
	}
	if err := s.Speed(1 * 1000 * 1000); err != nil {
		return nil, fmt.Errorf("max31855: speed error: %v", err)
	}
	return &Dev{s}, nil
}

// Temperature returns the themocouple temperature and the internal MAX31855 temperature (in that
// order).
func (d *Dev) Temperature() (devices.Celsius, devices.Celsius, error) {
	// Perform a 32-bit read of the device.
	var wBuf, rBuf [4]byte
	if err := d.spi.Tx(wBuf[:], rBuf[:]); err != nil {
		return 0, 0, fmt.Errorf("max31855: txn error: %v", err)
	}

	// Check for various errors.
	if rBuf[3]&1 != 0 {
		return 0, 0, fmt.Errorf("max31855: thermocouple open circuit error")
	}
	if rBuf[3]&2 != 0 {
		//fmt.Printf("%#02x %02x %02x %02x\n", rBuf[0], rBuf[1], rBuf[2], rBuf[3])
		return 0, 0, fmt.Errorf("max31855: thermocouple shorted to ground")
	}
	if rBuf[3]&4 != 0 {
		return 0, 0, fmt.Errorf("max31855: thermocouple shorted to VCC")
	}

	// Calculate internal temperature.
	intT := int32((int16(rBuf[2]) << 8) | int16(rBuf[3]&0xf0)) // sign-extension!
	intT = (intT * 1000) >> 8

	// Calculate thermocouple temperature.
	thermT := int32((int16(rBuf[0]) << 8) | int16(rBuf[1]&0xfc))
	thermT = (thermT * 1000) >> 4

	return devices.Celsius(thermT), devices.Celsius(intT), nil
}
