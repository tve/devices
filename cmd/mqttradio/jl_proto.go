// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import "github.com/tve/devices/sx1231"

// JeeLabs rfm69 protocol

const group = 6 // this needs to come from config!

// jlAck expects to get raw rfm69 packets using the JeeLabs format, looks whether they
// request an ack and if so publishes one.
func jlAck(sub chan RawRxMessage, pub pubFunc, debug LogPrintf) {
	for m := range sub {
		// Need to reconstruct a packet to decode it (TODO: refactor sx1231.JLDecode).
		pkt := sx1231.RxPacket{Payload: m.Payload.Packet}
		jlPkt, err := sx1231.JLDecode(group, &pkt)
		if err != nil {
			debug("Can't decode JL packet: %s", err)
			continue
		}
		if !jlPkt.Ack {
			debug("no ACK needed")
			continue // no ack requested
		}
		// Send an ack back.
		debug("ACK reply to node %d!", jlPkt.Src)
		txPkt := RawTxPacket{Packet: jlPkt.MakeAck(group)}
		pub(txPkt)
	}
}

func init() {
	RegisterModule(module{"jl-ack", jlAck})
}
