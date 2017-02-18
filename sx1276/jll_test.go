// Copyright 2016 by Thorsten von Eicken, see LICENSE file

package sx1276

import "testing"

var encodings = map[string]struct {
	hdr, node, kind byte
	toGW            bool
	rssi, fei       int
	payload         []byte
	pkt             []byte
}{
	"nil": {1, 2, 3, false, -44, 5, nil, []byte{0x62, 0x83, 120, 0}},
	"data-noack-4": {DataNoAck, 12, 69, true, -87, 5432, []byte{34, 35, 36, 37},
		[]byte{12, 197, 34, 35, 36, 37, 77, 42}},
	"data-ack-3": {DataAck, 11, 1, false, 0, 100, []byte{34, 35, 36},
		[]byte{0x6B, 1, 34, 35, 36}},
	"ack": {Ack, 23, 0, false, 1, -50000, nil, []byte{0xd7, 0x80, 127, 0x80}},
}

func Test_JLLEncode(t *testing.T) {
	for n, tc := range encodings {
		got := JLLEncode(tc.hdr, tc.toGW, tc.node, tc.kind, tc.payload, tc.rssi, tc.fei)
		if len(got) != len(tc.pkt) {
			t.Fatalf("Encoding %s length mismatch got %+v expected %+v", n, got, tc.pkt)
		}
		for i := range got {
			if got[i] != tc.pkt[i] {
				t.Fatalf("Encoding %s got %+v expected %+v", n, got, tc.pkt)
			}
		}
	}
}

func Test_JLLDecode(t *testing.T) {
	for n, tc := range encodings {
		got, err := JLLDecode(&RxPacket{Payload: tc.pkt})
		if err != nil {
			t.Fatalf("Unexpected error %v", err)
		}
		if got.Hdr != tc.hdr {
			t.Errorf("Decoding %s hdr mismatch, got %d expected %d", n, got.Hdr, tc.hdr)
		}
		if got.Node != tc.node {
			t.Errorf("Decoding %s node mismatch, got %d expected %d", n, got.Node, tc.node)
		}
		if got.Kind != tc.kind {
			t.Errorf("Decoding %s kind mismatch, got %d expected %d", n, got.Kind, tc.kind)
		}
		if got.ToGW != tc.toGW {
			t.Errorf("Decoding %s toGW mismatch, got %d expected %d", n, got.ToGW, tc.toGW)
		}

		tcRssi := tc.rssi
		if tcRssi != 0 && tcRssi > -37 {
			tcRssi = -37
		}
		if got.RemRSSI != tcRssi {
			t.Errorf("Decoding %s RSSI mismatch, got %d expected %d", n, got.RemRSSI, tcRssi)
		}

		tcFei := tc.fei
		switch {
		case tcRssi == 0:
			tcFei = 0
		case tcFei < -128*128:
			tcFei = -128 * 128
		default:
			tcFei = (tcFei / 128) * 128
		}
		if got.RemFEI != tcFei {
			t.Errorf("Decoding %s FEI mismatch, got %d expected %d", n, got.RemFEI, tcFei)
		}

		if len(got.Payload) != len(tc.payload) {
			t.Fatalf("Decoding %s length mismatch got %+v expected %+v",
				n, got.Payload, tc.payload)
		}
		for i := range got.Payload {
			if got.Payload[i] != tc.payload[i] {
				t.Errorf("Encoding %s got %+v expected %+v", n, got.Payload, tc.payload)
			}
		}
	}

	_, err := JLLDecode(nil)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
}
