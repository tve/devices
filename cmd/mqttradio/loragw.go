// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"fmt"

	"github.com/tve/devices/sx1276"
)

// This stuff is awaiting future functionality...

var kinds = []string{"D", "DA", "A", "?"}

func infoStr(pkt *sx1276.JLLRxPacket) string {
	if pkt.RemRSSI == 0 {
		return ""
	}
	return fmt.Sprintf("[%ddBm %dHz]", pkt.RemRSSI, pkt.RemFEI)
}
