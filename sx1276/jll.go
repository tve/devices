// Copyright 2016 by Thorsten von Eicken, see LICENSE file

package sx1276

import "fmt"

// JLLEncode encodes a JeeLabs LoRa (JLL) packet.
//
// A JLL packet consists of a header byte, a packet type byte, the payload, and
// optionally an info trailer. If the rssi parameter is 0 JLLEncode does not
// append the info trailer.
//
// The packet header byte is very similar to the rf12 JeeLabs header:
// http://jeelabs.org/2011/06/10/rf12-broadcasts-and-acks/index.html
// Bit 7 ctrl: 0=data 1=special.
// Bit 6 dest: 0=to-GW 1=from-GW.
// Bit 5 ack : 0=no-ack 1=ack-req.
// Bits 0-4: 32 nodes, 0=broadcast, 31=anonymous tx-only no-ack nodes.
// The following ctrl/ack combinations are used:
// c=0, a=0 : data, no ack requested.
// c=0, a=1 : data, ack requested.
// c=1, a=0 : ack.
// c=1, a=1 : special (undefined for now).
//
// The packet type byte indicates what the format of the payload is and supports
// 127 different payload formats using bits 0..6. The topmost bit is used to
// indicate whether an info trailer is present (value 1) or not (value 0).
// The info trailer consists of 2 bytes: min(RSSI[dBm]+164,127) and FEI[Hz]/128
// (signed) of the the most recent packet received from the other party.
// The top bit of the RSSI byte is unused.
//
// Two packet types are reserved:
//  0: empty packet, typically used for acks, may have an info trailer.
//  1: node details: Vstart[mV], Vend[mV], Temp[cC], PktSent, PktRecv, Pout[dBm],
//     Fadj[Hz], RSSIavg[dBm].
func JLLEncode(hdr byte, toGW bool, node byte, kind byte, payload []byte, rssi, fei int) []byte {
	pkt := make([]byte, len(payload)+4)
	// Header.
	pkt[0] = (hdr & 2 << 6) | (hdr & 1 << 5) | (node & 0x1f)
	if !toGW {
		pkt[0] |= 0x40
	}
	// Packet type.
	pkt[1] = kind & 0x7f
	copy(pkt[2:2+len(payload)], payload)
	if rssi == 0 {
		return pkt[0 : 2+len(payload)]
	}
	// Info trailer.
	pkt[1] |= 0x80
	i := 2 + len(payload)
	// RSSI.
	rssi += 164
	switch {
	case rssi > 127:
		pkt[i] = 127
	case rssi < 0:
		pkt[i] = 0
	default:
		pkt[i] = byte(rssi)
	}
	// FEI.
	fei /= 128
	switch {
	case fei > 127:
		pkt[i+1] = 127
	case fei < -128:
		pkt[i+1] = 0x80 // -128
	default:
		pkt[i+1] = byte(fei)
	}

	return pkt
}

// Packet kinds
const (
	DataNoAck = iota // data packet, no ack requested
	DataAck          // data packet, ack requested
	Ack              // ACK packet
	Special          // special packet, unused for now
)

// JLLRxPacket holds a decoded JeeLabs LoRa packet.
type JLLRxPacket struct {
	Hdr     byte // one of the 4 packet kinds
	ToGW    bool // direction: to/from gateway
	Node    byte // node number
	Kind    byte // packet type
	RemRSSI int  // rssi from trailer, 0 if none
	RemFEI  int  // fei from trailer, none if rssi==0
	RxPacket
}

// JLLDecode decodes a JeeLabs LoRa packet. See JLLEncode for a description of the
// packet format.
func JLLDecode(packet *RxPacket) (*JLLRxPacket, error) {
	if packet == nil {
		return nil, nil
	}
	jlPkt := JLLRxPacket{RxPacket: *packet}
	if len(packet.Payload) < 2 {
		return &jlPkt, fmt.Errorf("sx1276 JeeLabs LoRa decode: packet too short: %d bytes",
			len(packet.Payload))
	}

	jlPkt.Hdr = (jlPkt.Payload[0] & 0x80 >> 6) | (jlPkt.Payload[0] & 0x20 >> 5)
	jlPkt.ToGW = jlPkt.Payload[0]&0x40 == 0
	jlPkt.Node = jlPkt.Payload[0] & 0x1f
	jlPkt.Kind = jlPkt.Payload[1] & 0x7f
	if jlPkt.Payload[1]&0x80 == 0 || len(packet.Payload) < 4 {
		jlPkt.Payload = jlPkt.Payload[2:]
		//log.Printf("No info: %+v -> %+v", packet, jlPkt)
		return &jlPkt, nil // no info trailer
	}

	i := len(packet.Payload) - 2
	jlPkt.RemRSSI = int(packet.Payload[i]) - 164
	jlPkt.RemFEI = int(int8(packet.Payload[i+1])) * 128
	jlPkt.Payload = jlPkt.Payload[2:i]
	//log.Printf("W/info: %+v -> %+v", packet, jlPkt)
	return &jlPkt, nil
}

/*
type JLLTxPacket struct {
	Src, Dst byte // source and destination nodes for packet header
	Ack      bool // ACK request bit for packet header
	Payload  []byte
}

// IsAck returns true if the packet is an ACK.
func (p *JLRxPacket) IsAck() bool { return false }

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
