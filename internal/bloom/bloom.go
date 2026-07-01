// Package bloom implements a space-efficient probabilistic membership test.
//
// The filter uses the Kirsch–Mitzenmacher double-hashing technique to
// approximate k independent hash functions from a single FNV-1a-64 pass:
//
//	h_i(key) = (h1(key) + i·h2(key)) mod m
//
// where h1 = lower 32 bits of FNV-1a-64, h2 = upper 32 bits | 1 (kept odd
// to prevent cycling). This matches the approach used by LevelDB / RocksDB.
package bloom

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Filter is an in-memory Bloom filter.
type Filter struct {
	bits []uint64
	m    uint64 // total bit count
	k    uint64 // number of hash probes per key
}

// NewWithEstimate creates a filter sized for n expected items at target false-
// positive rate fp (e.g. 0.01 for 1 %). Typical: NewWithEstimate(100_000, 0.01).
func NewWithEstimate(n int, fp float64) *Filter {
	m := optimalM(n, fp)
	k := optimalK(m, n)
	return &Filter{bits: make([]uint64, (m+63)/64), m: m, k: k}
}

// New creates a filter with the given raw bit count and hash count.
func New(m, k uint64) *Filter {
	return &Filter{bits: make([]uint64, (m+63)/64), m: m, k: k}
}

// Add sets the bits for key.
func (f *Filter) Add(key []byte) {
	h1, h2 := hashKey(key)
	for i := uint64(0); i < f.k; i++ {
		bit := (h1 + i*h2) % f.m
		f.bits[bit>>6] |= 1 << (bit & 63)
	}
}

// MayContain returns false if key is definitely absent, or true if it might
// be present (with the false-positive probability the filter was built for).
func (f *Filter) MayContain(key []byte) bool {
	h1, h2 := hashKey(key)
	for i := uint64(0); i < f.k; i++ {
		bit := (h1 + i*h2) % f.m
		if f.bits[bit>>6]&(1<<(bit&63)) == 0 {
			return false
		}
	}
	return true
}

// Bytes serialises the filter for storage alongside an SSTable.
//
// Wire format:
//
//	k       uint64 LE   – number of hash functions
//	m       uint64 LE   – total bit count
//	bits    []uint64 LE – ceil(m/64) words
func (f *Filter) Bytes() []byte {
	out := make([]byte, 16+len(f.bits)*8)
	binary.LittleEndian.PutUint64(out[0:8], f.k)
	binary.LittleEndian.PutUint64(out[8:16], f.m)
	for i, w := range f.bits {
		binary.LittleEndian.PutUint64(out[16+i*8:], w)
	}
	return out
}

// FromBytes deserialises a filter previously produced by Bytes.
func FromBytes(b []byte) (*Filter, error) {
	if len(b) < 16 {
		return nil, fmt.Errorf("bloom: data too short (%d bytes)", len(b))
	}
	k := binary.LittleEndian.Uint64(b[0:8])
	m := binary.LittleEndian.Uint64(b[8:16])
	rest := b[16:]
	words := (m + 63) / 64
	if uint64(len(rest)) != words*8 {
		return nil, fmt.Errorf("bloom: bit-array length mismatch: want %d got %d", words*8, len(rest))
	}
	bits := make([]uint64, words)
	for i := range bits {
		bits[i] = binary.LittleEndian.Uint64(rest[i*8:])
	}
	return &Filter{bits: bits, m: m, k: k}, nil
}

// hashKey returns two independent 64-bit hash values derived from one FNV-1a-64
// pass. h2 is forced odd to prevent zero-stride cycles.
func hashKey(key []byte) (h1, h2 uint64) {
	const (
		offset64 uint64 = 14695981039346656037
		prime64  uint64 = 1099511628211
	)
	h := offset64
	for _, b := range key {
		h ^= uint64(b)
		h *= prime64
	}
	h1 = h & 0xFFFF_FFFF
	h2 = (h>>32) | 1 // keep odd
	return
}

func optimalM(n int, fp float64) uint64 {
	// m = ceil(-n·ln(fp) / ln(2)²)
	m := -float64(n) * math.Log(fp) / (math.Ln2 * math.Ln2)
	return uint64(math.Ceil(m))
}

func optimalK(m uint64, n int) uint64 {
	// k = round((m/n)·ln(2))
	k := float64(m) / float64(n) * math.Ln2
	if k < 1 {
		return 1
	}
	return uint64(math.Round(k))
}
