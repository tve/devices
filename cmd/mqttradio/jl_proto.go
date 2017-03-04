// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"fmt"

	"github.com/tve/devices/sx1231"
	"github.com/tve/devices/varint"
)

// JeeLabs rfm69 ACK protocol

const group = 6 // this needs to come from config!

// jlAck expects to get raw rfm69 packets using the JeeLabs format, looks whether they
// request an ack and if so publishes one. The ACK contains the node ID as dest, the GW ID
// as source, a byte with the RX RSSI (max(rssi+160, 127), and a byte with the FEI/128.
// The top bit of the rssi byte is effectively unused.
func jlAck(sub chan RawRxMessage, pub pubFunc, debug LogPrintf) {
	for m := range sub {
		src, _, ack, payload, err := sx1231.JLDecode(group, m.Payload.Packet)
		if err != nil {
			debug("Can't decode JL packet: %s", err)
			continue
		}
		if !ack {
			debug("no ACK needed")
			continue // no ack requested
		}
		// Send an ack back.
		debug("ACK reply to node %d!", src)
		ackPkt := sx1231.MakeJLAck(group, payload)
		rssi := m.Payload.Rssi + 164
		if rssi > 127 {
			rssi = 127
		}
		ackPkt = append(ackPkt, byte(rssi), byte(m.Payload.Fei/128))
		txPkt := RawTxPacket{Packet: ackPkt}
		pub("", txPkt)
	}
}

func init() {
	RegisterModule(module{"jl-ack", jlAck})
}

//=====

// jlDecode decodes packets using the JeeLabs protocol and having a type byte as the first byte in
// the payload. It publishes to a topic by adding "/<type>".
func jlDecode(sub chan RawRxMessage, pub pubFunc, debug LogPrintf) {
	for m := range sub {
		src, dst, ack, payload, err := sx1231.JLDecode(group, m.Payload.Packet)
		if err != nil {
			debug("Can't decode JL packet: %s", err)
			continue
		}
		if len(payload) < 1 {
			continue
		}
		txPkt := jlRxPacket{RawRxPacket: m.Payload, Src: src, Dst: dst, Ack: ack, Type: payload[0]}
		txPkt.Packet = payload[1:]
		topic := fmt.Sprintf("/%d", txPkt.Type)
		pub(topic, txPkt)
	}
}

func init() {
	RegisterModule(module{"jl-decode", jlDecode})
}

type jlRxPacket struct {
	RawRxPacket
	Src  byte
	Dst  byte
	Ack  bool
	Type byte `json:"type"`
}

type jlRxMessage struct {
	Topic   string
	Payload jlRxPacket
}

//=====

// varintDecode
func jlviDecode(sub chan jlRxMessage, pub pubFunc, debug LogPrintf) {
	for m := range sub {
		pub("", varintRxPacket{jlRxPacket: m.Payload, Data: varint.Decode(m.Payload.Packet)})
	}
}

func init() {
	RegisterModule(module{"jl-varint", jlviDecode})
}

type varintRxPacket struct {
	jlRxPacket
	Data []int
}
