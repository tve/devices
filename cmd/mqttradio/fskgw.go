// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import "github.com/tve/devices/sx1231"

func fskGW(radio *sx1231.Radio, tx <-chan message, rx chan<- message, logger LogPrintf) error {
	/*rxChan, txChan := radio.RxChan, radio.TxChan

	/*for pkt := range rxChan {
		jllPkt, err := sx1276.JLLDecode(pkt)
		if err != nil {
			log.Printf("Cannot JLL-decode: %q", pkt.Payload)
			continue
		}
		log.Printf("%2d: %s %db %ddB %ddBm %dHz %s",
			jllPkt.Node, hdrs[jllPkt.Hdr&3], len(jllPkt.Payload),
			jllPkt.Snr, jllPkt.Rssi, jllPkt.Fei,
			infoStr(jllPkt))
		if jllPkt.Hdr == sx1276.DataAck {
			txPkt := sx1276.JLLEncode(sx1276.Ack, false, jllPkt.Node, 0x80, nil,
				jllPkt.Rssi, jllPkt.Fei)
			txChan <- txPkt
		}
		if format, ok := fmtRegistry[jllPkt.Kind]; ok {
			log.Printf("    %s %s", format.name, format.toString(jllPkt.Payload))
		} else {
			log.Printf("    %d:%q", jllPkt.Kind, string(jllPkt.Payload))
		}
	}*/
	return nil
}
