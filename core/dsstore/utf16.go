package dsstore

import (
	"encoding/binary"
	"unicode/utf16"
)

func utf16beToString(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

func stringToUTF16BE(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		binary.BigEndian.PutUint16(b[i*2:], c)
	}
	return b
}
