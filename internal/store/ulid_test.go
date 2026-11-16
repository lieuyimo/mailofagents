package store

import (
	"testing"
	"time"
)

// ulidWithTail builds the ID a generator would emit for ms with the given
// random tail. Test helper for asserting carry behaviour.
func ulidWithTail(ms int64, tail [10]byte) string {
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	copy(b[6:], tail[:])
	return encodeULID(b)
}

// TestULIDGenTimeForward pins the ordinary path: when the timestamp moves
// forward, IDs must sort in that same order.
func TestULIDGenTimeForward(t *testing.T) {
	g := &ulidGen{}
	base := time.Unix(1730000000, 0)
	prev := g.next(base)
	for _, step := range []time.Duration{time.Millisecond, time.Second, time.Hour} {
		got := g.next(base.Add(step))
		if !IsULID(got) {
			t.Fatalf("not a ULID: %q", got)
		}
		if got <= prev {
			t.Fatalf("forward clock: %q does not sort after %q", got, prev)
		}
		prev = got
	}
}

// TestULIDGenSameMillisecond pins the load-bearing contract: bbolt iterates
// keys in byte order, so IDs generated within one millisecond must still be
// strictly increasing. Without monotonic mode a random tail orders them
// nondeterministically (the original red-baseline flake).
func TestULIDGenSameMillisecond(t *testing.T) {
	g := &ulidGen{}
	fixed := time.Unix(1730000000, 123_000_000)
	prev := g.next(fixed)
	for i := 0; i < 2000; i++ {
		got := g.next(fixed)
		if !IsULID(got) {
			t.Fatalf("call %d: not a ULID: %q", i, got)
		}
		if got <= prev {
			t.Fatalf("call %d: %q does not sort after %q", i, got, prev)
		}
		prev = got
	}
}

// TestULIDGenBackwardsClock pins the defence against a backwards clock step:
// a new ID must never sort before one emitted earlier.
func TestULIDGenBackwardsClock(t *testing.T) {
	g := &ulidGen{}
	later := time.Unix(1730000100, 0)
	first := g.next(later)
	second := g.next(later.Add(-time.Hour))
	if second <= first {
		t.Fatalf("backwards clock produced %q, which sorts before %q", second, first)
	}
}

// TestULIDGenTailCarry pins the 80-bit increment across a byte boundary.
func TestULIDGenTailCarry(t *testing.T) {
	ms := time.Unix(1730000000, 0).UnixMilli()
	g := &ulidGen{seeded: true, lastMS: ms}
	g.last[8] = 0x7F
	g.last[9] = 0xFF
	prev := ulidWithTail(ms, g.last)

	got := g.next(time.UnixMilli(ms))
	if got <= prev {
		t.Fatalf("carry produced %q, which does not sort after %q", got, prev)
	}
	if g.last[8] != 0x80 || g.last[9] != 0x00 {
		t.Fatalf("tail after carry = %x, want ...8000", g.last)
	}
}

// TestULIDGenTailOverflow pins the loud failure instead of a silent wrap:
// exhausting all 2^80 tail values in one millisecond must panic.
func TestULIDGenTailOverflow(t *testing.T) {
	ms := time.Unix(1730000000, 0).UnixMilli()
	g := &ulidGen{seeded: true, lastMS: ms}
	for i := range g.last {
		g.last[i] = 0xFF
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on monotonic tail overflow")
		}
	}()
	g.next(time.UnixMilli(ms))
}
