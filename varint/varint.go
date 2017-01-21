// Copyright 2016 by Thorsten von Eicken, see LICENSE file

package varint

// Encode encodes an array of signed ints into a buffer of varint bytes.
//
// Reference: http://jeelabs.org/article/1620c/
func Encode(arr []int) []byte {
	res := []byte{}
	for _, v := range arr {
		u := uint64(v << 1)
		switch {
		case v == 0:
			res = append(res, 0x80)
			continue
		case v < 0:
			u = ^u
		}
		var temp [10]byte
		var i int
		for i = 9; u != 0; i-- {
			temp[i] = byte(u & 0x7f)
			u >>= 7
		}
		temp[9] |= 0x80
		res = append(res, temp[i+1:]...)
	}
	return res
}

// Decode decodes buffer of varint bytes into an array of signed ints.
//
// Reference: http://jeelabs.org/article/1620c/
func Decode(buf []byte) []int {
	res := []int{}
	val := 0
	for i := range buf {
		val = (val << 7) | int(buf[i]&0x7f)
		if buf[i]&0x80 != 0 {
			if val&1 == 0 {
				val = int(uint64(val) >> 1)
			} else {
				val = int(^(uint64(val) >> 1))
			}
			res = append(res, val)
			val = 0
		}
	}
	return res
}
