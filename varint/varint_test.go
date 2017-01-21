// Copyright 2016 by Thorsten von Eicken, see LICENSE file

package varint

import "testing"

var varinttests = map[string]struct {
	dec []int
	enc []byte
}{
	"empty": {[]int{}, []byte{}},
	"small": {[]int{0, 1, 2, -1, -2}, []byte{0x80, 0x82, 0x84, 0x81, 0x83}},
	"positive": {
		[]int{63, 64, 127,
			(12 << 6) + 34, (12 << 13) + 34, 0x7f << 56},
		[]byte{0xfe, 0x1, 0x80, 1, 0xfe,
			12, 128 + 68, 12, 0, 0x80 + 68, 1, 0x7e, 0, 0, 0, 0, 0, 0, 0, 0x80}},
	"negative": {
		[]int{-64, -65, -127, -128,
			-(12 << 6) + 34, -(12 << 13) + 34, -9223372036854775808},
		[]byte{0xff, 0x1, 0x81, 1, 0xfd, 1, 0xff,
			11, 187, 11, 0x7f, 187, 1, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0xff}},
}

func TestEncode(t *testing.T) {
	for n, tc := range varinttests {
		got := Encode(tc.dec)
		if len(got) != len(tc.enc) {
			t.Fatalf("Encoding '%s'\n%+v length mismatch got\n%+v expected\n%+v",
				n, tc.dec, got, tc.enc)
		}
		for i := range got {
			if got[i] != tc.enc[i] {
				t.Fatalf("Encoding %s got\n%+v expected\n%+v", n, got, tc.enc)
			}
		}
	}
}

func TestDencode(t *testing.T) {
	for n, tc := range varinttests {
		got := Decode(tc.enc)
		if len(got) != len(tc.dec) {
			t.Fatalf("Decoding '%s'\n%+v length mismatch got\n%+v expected\n%+v",
				n, tc.enc, got, tc.dec)
		}
		for i := range got {
			if got[i] != tc.dec[i] {
				t.Fatalf("Decoding %s got\n%+v expected\n%+v", n, got, tc.dec)
			}
		}
	}
}
