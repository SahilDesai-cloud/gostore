// Package sstable implements immutable, sorted on-disk tables (SSTables).
//
// On-disk layout
// ──────────────
//
//	┌──────────────────────────────────────────────────────────┐
//	│  DATA SECTION                                            │
//	│  Sequence of key-value entries sorted by key:           │
//	│    key_len   uint32 LE                                   │
//	│    key       [key_len]byte                               │
//	│    val_len   uint32 LE  (0 for tombstones)              │
//	│    val       [val_len]byte                               │
//	│    kind      uint8  — 0=KindValue, 1=KindTombstone      │
//	├──────────────────────────────────────────────────────────┤
//	│  INDEX SECTION  (sparse: one entry every IndexInterval) │
//	│    key_len   uint32 LE                                   │
//	│    key       [key_len]byte                               │
//	│    offset    uint64 LE  — byte offset in DATA SECTION   │
//	├──────────────────────────────────────────────────────────┤
//	│  BLOOM SECTION                                           │
//	│    serialised bloom.Filter bytes (see bloom package)     │
//	├──────────────────────────────────────────────────────────┤
//	│  FOOTER  (48 bytes, always at end of file)              │
//	│    data_len   uint64 LE  — size of data section         │
//	│    index_off  uint64 LE  — offset of index section      │
//	│    index_len  uint64 LE  — size of index section        │
//	│    bloom_off  uint64 LE  — offset of bloom section      │
//	│    bloom_len  uint64 LE  — size of bloom section        │
//	│    magic      [8]byte    — "GOSTORE1"                   │
//	└──────────────────────────────────────────────────────────┘
package sstable

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/SahilDesai-cloud/gostore/internal/bloom"
	"github.com/SahilDesai-cloud/gostore/internal/record"
)

// IndexInterval is the stride of the sparse index: one index entry per N data
// entries. Matches LevelDB's default.
const IndexInterval = 128

var magic = [8]byte{'G', 'O', 'S', 'T', 'O', 'R', 'E', '1'}

// Writer builds a single SSTable from a sorted stream of entries.
type Writer struct {
	f          *os.File
	bw         *bufio.Writer
	bitsPerKey int
	idxEntries []indexEntry
	keys       [][]byte // accumulated for bloom filter
	dataOff    uint64   // bytes written to data section so far
	count      int
}

type indexEntry struct {
	key    []byte
	offset uint64 // data-section byte offset
}

// NewWriter creates or overwrites the SSTable at path.
// bitsPerKey controls bloom-filter density (10 ≈ 1 % FPR); pass 0 to skip.
func NewWriter(path string, bitsPerKey int) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("sstable create %s: %w", path, err)
	}
	return &Writer{
		f:          f,
		bw:         bufio.NewWriterSize(f, 1<<20),
		bitsPerKey: bitsPerKey,
	}, nil
}

// Add appends one entry. Entries MUST be supplied in ascending key order.
func (w *Writer) Add(key, value []byte, kind record.Kind) error {
	if w.count%IndexInterval == 0 {
		w.idxEntries = append(w.idxEntries, indexEntry{
			key:    cloneBytes(key),
			offset: w.dataOff,
		})
	}
	if w.bitsPerKey > 0 {
		w.keys = append(w.keys, cloneBytes(key))
	}
	n, err := writeEntry(w.bw, key, value, kind)
	if err != nil {
		return fmt.Errorf("sstable add: %w", err)
	}
	w.dataOff += uint64(n)
	w.count++
	return nil
}

// Finish flushes the data section, then writes the index, bloom filter, and
// footer. The file is synced to disk before returning.
func (w *Writer) Finish() error {
	// ── 1. Flush buffered data section ──────────────────────────────────────
	if err := w.bw.Flush(); err != nil {
		return fmt.Errorf("sstable finish flush data: %w", err)
	}
	dataLen := w.dataOff
	indexOff := dataLen

	// ── 2. Write sparse index section directly to file ──────────────────────
	ibw := bufio.NewWriterSize(w.f, 256<<10)
	var indexLen uint64
	for _, ie := range w.idxEntries {
		n, err := writeIndexEntry(ibw, ie.key, ie.offset)
		if err != nil {
			return fmt.Errorf("sstable write index: %w", err)
		}
		indexLen += uint64(n)
	}
	if err := ibw.Flush(); err != nil {
		return fmt.Errorf("sstable flush index: %w", err)
	}

	bloomOff := indexOff + indexLen

	// ── 3. Build and write bloom filter ─────────────────────────────────────
	var bloomLen uint64
	if w.bitsPerKey > 0 && len(w.keys) > 0 {
		m := uint64(w.bitsPerKey) * uint64(len(w.keys))
		if m < 64 {
			m = 64
		}
		// k = (m/n)·ln(2); same formula as optimalK in bloom package.
		k := uint64(math.Round(float64(m) / float64(len(w.keys)) * math.Ln2))
		if k < 1 {
			k = 1
		}
		bf := bloom.New(m, k)
		for _, key := range w.keys {
			bf.Add(key)
		}
		data := bf.Bytes()
		if _, err := w.f.Write(data); err != nil {
			return fmt.Errorf("sstable write bloom: %w", err)
		}
		bloomLen = uint64(len(data))
	}

	// ── 4. Write footer ──────────────────────────────────────────────────────
	var foot [48]byte
	binary.LittleEndian.PutUint64(foot[0:8], dataLen)
	binary.LittleEndian.PutUint64(foot[8:16], indexOff)
	binary.LittleEndian.PutUint64(foot[16:24], indexLen)
	binary.LittleEndian.PutUint64(foot[24:32], bloomOff)
	binary.LittleEndian.PutUint64(foot[32:40], bloomLen)
	copy(foot[40:48], magic[:])
	if _, err := w.f.Write(foot[:]); err != nil {
		return fmt.Errorf("sstable write footer: %w", err)
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("sstable sync: %w", err)
	}
	return w.f.Close()
}

// Close closes the underlying file without finishing. Use only in error paths.
func (w *Writer) Close() error { return w.f.Close() }

// writeEntry writes one data-section entry and returns bytes written.
func writeEntry(bw *bufio.Writer, key, value []byte, kind record.Kind) (int, error) {
	var total int
	n, err := writeU32(bw, uint32(len(key)))
	if err != nil {
		return 0, err
	}
	total += n
	if _, err := bw.Write(key); err != nil {
		return 0, err
	}
	total += len(key)
	n, err = writeU32(bw, uint32(len(value)))
	if err != nil {
		return 0, err
	}
	total += n
	if _, err := bw.Write(value); err != nil {
		return 0, err
	}
	total += len(value)
	if err := bw.WriteByte(byte(kind)); err != nil {
		return 0, err
	}
	total++
	return total, nil
}

// writeIndexEntry writes one sparse-index entry and returns bytes written.
func writeIndexEntry(bw *bufio.Writer, key []byte, offset uint64) (int, error) {
	var total int
	n, err := writeU32(bw, uint32(len(key)))
	if err != nil {
		return 0, err
	}
	total += n
	if _, err := bw.Write(key); err != nil {
		return 0, err
	}
	total += len(key)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], offset)
	if _, err := bw.Write(buf[:]); err != nil {
		return 0, err
	}
	total += 8
	return total, nil
}

func writeU32(bw *bufio.Writer, v uint32) (int, error) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	return bw.Write(buf[:])
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
