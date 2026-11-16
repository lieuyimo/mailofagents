package store

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"
)

// ULID is a 26-character Crockford Base32 string encoding a 128-bit value:
// 48 bits of millisecond timestamp + 80 bits of cryptographic randomness.
// It is time-ordered (so lexical sort matches chronological sort) and
// collision-resistant without coordination — ideal for message IDs stored in
// a bbolt bucket whose iteration order matters.
//
// This is a minimal pure-Go implementation with no external dependency. Calls
// are monotonic: two IDs generated in the same millisecond (or after a
// backwards clock step) get the previous timestamp and an incremented random
// tail, so byte order still equals generation order. That is a load-bearing
// property, not a nicety — inbox/sent indexes, showcase eviction, and thread
// ordering all rely on "ULID key order = chronological order".
//
// See https://github.com/ulid/spec for the format.

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// IsULID reports whether s is a 26-char Crockford Base32 string (the exact
// alphabet newULID emits — single source of truth for write-side format
// checks). A hand-rolled range check once dropped 'Q' between N-P and R-T
// and rejected ~56% of REAL ids flakily; deriving from the constant makes
// that class of bug impossible.
func IsULID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(crockford, rune(s[i])) {
			return false
		}
	}
	return true
}

// ulidGen is one monotonic ULID generator. The process shares defaultULIDGen;
// tests construct their own so they never depend on state left by earlier
// tests.
type ulidGen struct {
	mu     sync.Mutex
	lastMS int64
	last   [10]byte // 80-bit random tail of the most recent ID
	seeded bool
}

// defaultULIDGen is the process-wide generator. It is mutex-guarded so
// concurrent request handlers still receive IDs in a single generation order.
var defaultULIDGen ulidGen

// newULID returns a fresh ULID string for the current time.
func newULID() string {
	return defaultULIDGen.next(time.Now())
}

// next returns a ULID string for the given time.
//
// Implements the ULID spec's monotonic mode: if the supplied time is not
// strictly after the previous call's millisecond, the previous timestamp is
// reused and the 80-bit random tail is incremented by one. Within one process,
// IDs are therefore strictly increasing in byte/lexical order in generation
// order, even for same-millisecond bursts and backwards clock steps.
// (Generation order, not transaction-commit order: a concurrent writer may
// commit in another order. A restart with a backwards clock starts a fresh
// generator and provides no such guarantee across the restart.)
func (g *ulidGen) next(now time.Time) string {
	var b [16]byte
	ms := now.UnixMilli()

	g.mu.Lock()
	if g.seeded && ms <= g.lastMS {
		// Same millisecond (or clock moved backwards): keep the last
		// timestamp so the embedded time never decreases, and bump the
		// random tail so this ID still sorts after the previous one.
		ms = g.lastMS
		carried := true
		for i := len(g.last) - 1; i >= 0; i-- {
			g.last[i]++
			if g.last[i] != 0 {
				carried = false
				break
			}
		}
		if carried {
			g.mu.Unlock()
			// All 2^80 tail values in this millisecond are exhausted.
			// Silently wrapping would break the strict-ordering promise,
			// so fail loudly instead.
			panic("store: ULID monotonic random tail overflowed")
		}
	} else {
		if _, err := rand.Read(g.last[:]); err != nil {
			g.mu.Unlock()
			// crypto/rand failure is catastrophic for ID uniqueness.
			// Panicking is safer than silently emitting a predictable ID.
			panic("store: crypto/rand failed during ULID generation: " + err.Error())
		}
	}
	g.lastMS = ms
	g.seeded = true
	rnd := g.last
	g.mu.Unlock()

	// 48-bit big-endian millisecond timestamp (the low 6 bytes of ms).
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	// 80 bits, monotonic within a millisecond.
	copy(b[6:], rnd[:])
	return encodeULID(b)
}

// newULIDAt is a convenience wrapper around the process-wide generator,
// kept for callers/tests that pin the timestamp.
func newULIDAt(now time.Time) string {
	return defaultULIDGen.next(now)
}

// encodeULID encodes 16 bytes into the 26-character Crockford Base32 form.
func encodeULID(b [16]byte) string {
	// ULID encodes the 48-bit time as 10 chars and the 80-bit random as 16
	// chars, for 26 total. We process 5-bit groups left to right.
	dst := make([]byte, 26)
	// Time part: 6 bytes -> 10 chars (top 4 bits of first char are zero).
	dst[0] = crockford[(b[0]&224)>>5]
	dst[1] = crockford[b[0]&31]
	dst[2] = crockford[(b[1]&248)>>3]
	dst[3] = crockford[((b[1]&7)<<2)|((b[2]&192)>>6)]
	dst[4] = crockford[(b[2]&62)>>1]
	dst[5] = crockford[((b[2]&1)<<4)|((b[3]&240)>>4)]
	dst[6] = crockford[((b[3]&15)<<1)|((b[4]&128)>>7)]
	dst[7] = crockford[(b[4]&124)>>2]
	dst[8] = crockford[((b[4]&3)<<3)|((b[5]&224)>>5)]
	dst[9] = crockford[b[5]&31]
	// Random part: 10 bytes -> 16 chars, processed as 5-bit groups.
	r := b[6:16] // 80 bits
	var bits uint64
	var n uint
	j := 10
	for i := 0; i < len(r); i++ {
		bits = (bits << 8) | uint64(r[i])
		n += 8
		for n >= 5 {
			n -= 5
			dst[j] = crockford[(bits>>n)&31]
			j++
		}
	}
	// One trailing group from the remaining bits (n < 5, padded with zeros).
	if n > 0 {
		dst[j] = crockford[(bits<<(5-n))&31]
	}
	return string(dst)
}
