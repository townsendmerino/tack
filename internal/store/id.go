package store

import (
	"crypto/rand"
	"encoding/binary"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewSessionID returns "s_" and a ULID: 48 bits of milliseconds then 80 random bits, in Crockford base32, so ids sort
// by creation time.
func NewSessionID(now time.Time) proto.SessionID {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(now.UnixMilli())<<16)
	_, _ = rand.Read(b[6:])
	// 128 bits as 26 base32 digits: the first digit holds the top 3 bits.
	out := make([]byte, 26)
	hi, lo := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return proto.SessionID("s_" + string(out))
}
