// Copyright 2016 by Thorsten von Eicken, see LICENSE file

package rfm69

// NewJL creates a standard RFM69 Radio object and wraps it with packet processing to match
// the JeeLabs "native" rfm69 packet format. The format is ill-documented but as follows.
//
// Jeelabs native rfm69 packet format
//
// Preamble: 5 bytes, sync bytes: 2, packet data: 1 byte destination, 1 byte source, 0..62
// bytes data, std CRC
//
// The first sync byte is 0x2d, the second is the group ID (network number).
// The first payload data byte contains the 6-bit destination node ID and two sync parity bits
// at the top. Bit 7 (MSB) is calculated as the group's b7^b5^b3^b1 and bit 6 as the group's
// b6^b4^b2^b0.
// The second payload byte contains the 6-bit source node ID and two control bits. Bit 7 is an
// ACK request bit and bit 6 is unassigned.
// A packet with destination ID 0 is a broadcast packet, a node ID of 62 is used for
// anonymouns tx-only nodes, and a node ID of 63 is used on the receiving end to denote a node
// that receives all packets regardless of destination (promiscuous mode).
