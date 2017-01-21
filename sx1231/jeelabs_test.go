// Copyright 2016 by Thorsten von Eicken, see LICENSE file

package sx1231

import "testing"

var encodings = map[string]struct {
	grp, src, dst byte
	ack           bool
	payload       []byte
	result        []byte
}{
	"3bt": {1, 2, 3, true, []byte{5, 6, 7}, []byte{67, 130, 5, 6, 7}},
	"0bf": {62, 2, 1, false, []byte{}, []byte{129, 2}},
	"10bf": {2, 6, 7, false, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		[]byte{135, 6, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
}

func Test_JLEncode(t *testing.T) {
	for n, tc := range encodings {
		got := JLEncode(tc.grp, tc.src, tc.dst, tc.ack, tc.payload)
		if len(got) != len(tc.result) {
			t.Fatalf("Encoding %s length mismatch got %+v expected %+v", n, got, tc.result)
		}
		for i := range got {
			if got[i] != tc.result[i] {
				t.Fatalf("Encoding %s got %+v expected %+v", n, got, tc.result)
			}
		}
	}
}

func Test_JLDecode(t *testing.T) {
	for n, tc := range encodings {
		got, err := JLDecode(tc.grp, &RxPacket{Payload: tc.result})
		if err != nil {
			t.Fatalf("Unexpected error %v", err)
		}
		if got.Src != tc.src {
			t.Fatalf("Decoding %s src mismatch, got %d expected %d", n, got.Src, tc.src)
		}
		if got.Dst != tc.dst {
			t.Fatalf("Decoding %s dst mismatch, got %d expected %d", n, got.Dst, tc.dst)
		}
		if got.Ack != tc.ack {
			t.Fatalf("Decoding %s ack mismatch, got %d expected %d", n, got.Ack, tc.ack)
		}
		if len(got.Payload) != len(tc.payload) {
			t.Fatalf("Decoding %s length mismatch got %+v expected %+v",
				n, got.Payload, tc.payload)
		}
		for i := range got.Payload {
			if got.Payload[i] != tc.payload[i] {
				t.Fatalf("Encoding %s got %+v expected %+v", n, got.Payload, tc.payload)
			}
		}
	}
}
