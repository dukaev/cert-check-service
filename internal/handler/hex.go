package handler

import "errors"

// maxSerialBytes bounds the on-stack hex decode buffer. RFC 5280 §4.1.2.2
// caps CA serial numbers at 20 octets — 64 leaves comfortable headroom.
const maxSerialBytes = 64

const (
	hexAlphabetUpper = "0123456789ABCDEF"
	hexAlphabetLower = "0123456789abcdef"
)

// decodeHexInto decodes a hex string into dst and returns the byte count.
// Caller guarantees len(dst) >= len(s)/2.
func decodeHexInto(dst []byte, s string) (int, error) {
	if len(s)%2 != 0 {
		return 0, errors.New("odd length")
	}
	for i := 0; i < len(s); i += 2 {
		hi, ok1 := fromHexChar(s[i])
		lo, ok2 := fromHexChar(s[i+1])
		if !ok1 || !ok2 {
			return 0, errors.New("not hex")
		}
		dst[i/2] = hi<<4 | lo
	}
	return len(s) / 2, nil
}

func fromHexChar(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// encodeHexUpper writes hex(src) in upper-case into dst. Caller guarantees
// len(dst) >= 2*len(src).
func encodeHexUpper(dst, src []byte) {
	for i, b := range src {
		dst[i*2] = hexAlphabetUpper[b>>4]
		dst[i*2+1] = hexAlphabetUpper[b&0x0F]
	}
}
