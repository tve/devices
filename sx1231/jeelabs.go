// Copyright 2016 by Thorsten von Eicken, see LICENSE file

package sx1231

import "fmt"

// JeeLabsSync returns a byte array of sync bytes given the group number.
func JeeLabsSync(grp byte) []byte {
	return []byte{0x2d, grp}
}

// JLEncode encodes a JeeLabs packet.
//
// Jeelabs native rfm69 packet format
//
// Preamble: 5 bytes, sync bytes: 2, packet data: 1 byte destination, 1 byte source, 0..62
// bytes data, std CRC. The first sync byte is 0x2d, the second is the group ID (network number).
//
// The first payload data byte contains the 6-bit destination node ID and two sync parity bits
// at the top. Bit 7 (MSB) is calculated as the group's b7^b5^b3^b1 and bit 6 as the group's
// b6^b4^b2^b0.
//
// The second payload byte contains the 6-bit source node ID and two control bits. Bit 7 is an
// ACK request bit and bit 6 is unassigned.
//
// A packet with destination ID 0 is a broadcast packet, a node ID of 62 is used for
// anonymouns tx-only nodes, and a node ID of 63 is used on the receiving end to denote a node
// that receives all packets regardless of destination (promiscuous mode).
func JLEncode(grp, src, dst byte, ack bool, payload []byte) []byte {
	p7 := ((grp >> 7) & 1) ^ ((grp >> 5) & 1) ^ ((grp >> 3) & 1) ^ ((grp >> 1) & 1)
	p6 := ((grp >> 6) & 1) ^ ((grp >> 4) & 1) ^ ((grp >> 2) & 1) ^ ((grp >> 0) & 1)
	a := byte(0)
	if ack {
		a = 0x80
	}
	p := make([]byte, len(payload)+2)
	p[0] = (dst & 0x3f) | (p7 << 7) | (p6 << 6)
	p[1] = (src & 0x3f) | a
	copy(p[2:], payload)
	return p
}

// MakeJLAck returns an ACK packet given a received payload with the ack bit set.
func MakeJLAck(grp byte, payload []byte) []byte {
	if src, dst, ack, _, err := JLDecode(grp, payload); err == nil && ack {
		return JLEncode(grp, dst, src, false, nil)
	}
	return nil
}

// JLDecode decodes a JeeLabs packet. See JLEncode for a description of the
// packet format. The outPayload it returns has the src/dst stripped.
func JLDecode(grp byte, payload []byte) (
	src, dst byte, ack bool, outPayload []byte, err error,
) {
	if len(payload) < 2 {
		err = fmt.Errorf("sx1231 JeeLabs decode: packet too short: %d bytes",
			len(payload))
		return
	}

	// check group parity bits.
	p7 := ((grp >> 7) & 1) ^ ((grp >> 5) & 1) ^ ((grp >> 3) & 1) ^ ((grp >> 1) & 1)
	p6 := ((grp >> 6) & 1) ^ ((grp >> 4) & 1) ^ ((grp >> 2) & 1) ^ ((grp >> 0) & 1)
	if payload[0]&0xc0 != (p7<<7)|(p6<<6) {
		err = fmt.Errorf(
			"sx1231 JeeLabs decode: bad group parity: got %#x want %#x for group %d",
			payload[0]&0xc0, (p7<<7)|(p6<<6), grp)
		return
	}

	dst = payload[0] & 0x3f
	src = payload[1] & 0x3f
	ack = payload[1]&0x80 != 0
	outPayload = payload[2:]
	return
}

/* deprecated in favor of mqttradio GW type of structure

// JLRxPacket holds a decoded JeeLabs packet.
type JLRxPacket struct {
	Src, Dst byte // source and destination nodes from packet header
	Ack      bool // ACK request bit from packet header
	RxPacket
}

type JLTxPacket struct {
	Src, Dst byte // source and destination nodes for packet header
	Ack      bool // ACK request bit for packet header
	Payload  []byte
}

// JLAckHandler spawns a goroutine that mediates the TX and RX channels in order to retransmit
// outgoing packets that are unacked and to reply with ACKs when incoming packets request so.
func JLAckHandler(radio *Radio, grp byte) (chan<- *JLTxPacket, <-chan *JLRxPacket) {
	txChan := make(chan *JLTxPacket, 1)
	rxChan := make(chan *JLRxPacket, 1)
	go func() {
		waitingForAck := false
		for {
			if waitingForAck {
				// Ignore txChan until we get ack or give up.
				select {
				case rxPkt := <-radio.RxChan:
					jlPkt, err := JLDecode(grp, rxPkt)
					if err != nil {
						radio.log("%s", err.Error())
						break
					}
					if jlPkt.IsAck() {
						waitingForAck = false
						break
					}
					rxChan <- jlPkt
				}
			} else {
				// Handle both txChan and rxChan.
				select {
				case txPkt := <-txChan:
					jlPkt := JLEncode(grp, txPkt.Src, txPkt.Dst, txPkt.Ack, txPkt.Payload)
					radio.txChan <- jlPkt
					waitingForAck = txPkt.Ack
				case rxPkt := <-radio.RxChan:
					jlPkt, err := JLDecode(grp, rxPkt)
					if err != nil {
						radio.log("%s", err.Error())
						break
					}
					rxChan <- jlPkt
				}
			}
		}
	}()
	return txChan, rxChan
}

*/
