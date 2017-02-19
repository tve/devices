// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/tve/devices/varint"
)

var fmtRegistry = map[byte]format{
	10: format{"gpsNav", gpsNavToString, gpsNavToMqtt}, // gps navigation
}

type format struct {
	name     string
	toString func([]byte) string           // pretty-print
	toMqtt   func([]byte) (string, string) // topic, data
}

// gpsNavToString decodes a GPS Navigation message to a string.
//
// The format is:
// 1. UTC time as HHMMSS.SSS
// 2. A/V "OK"/"WARN" flag
// 3/4. latitude/longitude with 4 fractional digits
// 5. speed in 1/10000 knots
// 6. course in 1/10000 degrees
// 7. date as YYMMDD

func gpsNavToString(pkt []byte) string {
	if len(pkt) == 0 {
		return "<empty>"
	}
	data := varint.Decode(pkt)
	if len(data) != 8 {
		return printNumbers(data)
	}

	status := "WARN"
	if data[1] == 'A' {
		status = "OK"
	}
	t := fixGpsDateTime(data[6], data[0])
	return fmt.Sprintf("%s %s <%.6f %.6f> %.4fkts %.1f° mag%.1f°",
		t.Format("2006-01-02 15:04:05.000"), status,
		float64(data[2])/1000000, float64(data[3])/1000000,
		float64(data[4])/10000, float64(data[5])/10000,
		float64(data[7])/10000)
}

func gpsNavToMqtt([]byte) (string, string) {
	return "", ""
}

func printNumbers(data []int) string {
	str := ""
	for i := 0; i < len(data); i++ {
		if i > 0 {
			str += ", "
		}
		str += strconv.Itoa(data[i])
	}
	return str
}

func fixGpsDateTime(d, t int) time.Time {
	t1 := t / 1000
	return time.Date(2000+d%100, time.Month((d/100)%100), d/10000,
		t1/10000, (t1/100)%100, t1%100, (t%1000)*1000000, time.UTC)
}

/*
   f3>pkt           \ UTC time
   c>pkt            \ A/V flag
   [char] S dmf>pkt \ latitude with N/S sign
   [char] W dmf>pkt \ latitude with E/W sign
   f4>pkt           \ speed knts
   f4>pkt           \ course made good
   n>pkt            \ date
   [char] W f4d>pkt \ magnetic variation E/W
   drop
*/
