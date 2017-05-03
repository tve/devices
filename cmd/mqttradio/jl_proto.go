// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"fmt"

	"github.com/tve/devices/sx1231"
	"github.com/tve/devices/varint"
)

// JeeLabs rfm69 ACK protocol

const group = 6 // this needs to come from config!

// jlAck takes a raw rfm69 packet using the JeeLabs format, looks whether it
// requests an ack and if so publishes one. The ACK contains the node ID as dest, the GW ID
// as source, a byte with the RX SNR (0..63), and a byte with the FEI/128.
// The top 2 bits of the rssi byte are effectively unused.
func jlAck(m *RawRxMessage, pub pubFunc, debug LogPrintf) {
	src, _, ack, payload, err := sx1231.JLDecode(group, m.Payload.Packet)
	if err != nil {
		debug("Can't decode JL packet: %s", err)
		return
	}
	if !ack {
		debug("no ACK needed")
		return // no ack requested
	}
	// Send an ack back.
	debug("ACK reply to node %d!", src)
	ackPkt := sx1231.MakeJLAck(group, payload)
	snr := m.Payload.Snr
	switch {
	case snr < 0:
		snr = 0
	case snr > 63:
		snr = 63
	}
	ackPkt = append(ackPkt, byte(snr), byte(m.Payload.Fei/128))
	txPkt := RawTxPacket{Packet: ackPkt}
	pub("", txPkt)
}

func init() {
	RegisterModule(module{"jl-ack", jlAck})
}

//=====

// jlDecode decodes a packet using the JeeLabs protocol and having a type byte as the first byte in
// the payload. It publishes to a topic by adding "/<type>".
func jlDecode(m *RawRxMessage, pub pubFunc, debug LogPrintf) {
	src, dst, ack, payload, err := sx1231.JLDecode(group, m.Payload.Packet)
	if err != nil {
		debug("Can't decode JL packet: %s", err)
		return
	}
	if len(payload) < 1 {
		return
	}
	txPkt := jlRxPacket{RawRxPacket: m.Payload, Src: src, Dst: dst, Ack: ack, Type: payload[0]}
	txPkt.Packet = payload[1:]
	topic := fmt.Sprintf("/%d", txPkt.Type)
	pub(topic, txPkt)
}

func init() {
	RegisterModule(module{"jl-decode", jlDecode})
}

type jlRxPacket struct {
	RawRxPacket
	Src  byte `json:"src"`
	Dst  byte `json:"dst"`
	Ack  bool `json:"ack"`
	Type byte `json:"type"`
}

type jlRxMessage struct {
	Topic   string
	Payload jlRxPacket
}

//=====

// jlviDecode decodes varints in the payload of a packet.
func jlviDecode(m *jlRxMessage, pub pubFunc, debug LogPrintf) {
	pub("", varintRxPacket{jlRxPacket: m.Payload, Data: varint.Decode(m.Payload.Packet)})
}

func init() {
	RegisterModule(module{"jl-varint", jlviDecode})
}

type varintRxPacket struct {
	jlRxPacket
	Data []int `json:"data"`
}
