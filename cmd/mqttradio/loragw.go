// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"fmt"
	"log"

	"github.com/tve/devices/sx1276"
)

func loraGW(radio *sx1276.Radio, tx <-chan message, rx chan<- message, debug LogPrintf) {

	// Radio -> MQTT goroutine.
	go func() {
		for pkt := range radio.RxChan {
			// Decode the packet.
			jllPkt, err := sx1276.JLLDecode(pkt)
			if err != nil {
				log.Printf("cannot JLL-decode: %q", pkt.Payload)
				continue
			}
			// Send an ACK back, if requested.
			if jllPkt.Kind == sx1276.DataAck {
				txPkt := sx1276.JLLEncode(sx1276.Ack, false, jllPkt.Node, 0x80, nil,
					jllPkt.Rssi, jllPkt.Fei)
				radio.TxChan <- txPkt
			}
			// Push the packet to MQTT, if appropriate.
			if jllPkt.Kind == sx1276.DataNoAck || jllPkt.Kind == sx1276.DataAck {
				rx <- message{node: jllPkt.Node,
					payload: append([]byte{jllPkt.Fmt}, jllPkt.Payload...)}
			}
			// Print info about the packet.
			if debug == nil {
				continue
			}
			debug("%2d: %s %db %ddB %ddBm %dHz %s",
				jllPkt.Node, kinds[jllPkt.Kind&3], len(jllPkt.Payload),
				jllPkt.Snr, jllPkt.Rssi, jllPkt.Fei,
				infoStr(jllPkt))
			if format, ok := fmtRegistry[jllPkt.Fmt]; ok {
				debug("    %s %s", format.name, format.toString(jllPkt.Payload))
			} else {
				debug("    %d:%q", jllPkt.Fmt, string(jllPkt.Payload))
			}
		}
		log.Printf("radio->mqtt goroutine exiting")
	}()

	go func() {
		for msg := range tx {
			if len(msg.payload) < 1 {
				log.Printf("got message with empty payload from mqtt")
				continue
			}
			pkt := sx1276.JLLEncode(sx1276.DataNoAck, false, msg.node,
				msg.payload[0], msg.payload[1:], 0, 0)
			radio.TxChan <- pkt
			// Print info about packet.
			if debug == nil {
				continue
			}
			debug("%0d: TX %d\n", msg.node, msg.payload[0])
		}
		log.Printf("mqtt->radio goroutine exiting")
	}()
}

var kinds = []string{"D", "DA", "A", "?"}

func infoStr(pkt *sx1276.JLLRxPacket) string {
	if pkt.RemRSSI == 0 {
		return ""
	}
	return fmt.Sprintf("[%ddBm %dHz]", pkt.RemRSSI, pkt.RemFEI)
}
