package sstable

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/SahilDesai-cloud/gostore/internal/bloom"
	"github.com/SahilDesai-cloud/gostore/internal/record"
)

const footerSize = 48

// memIndex is the in-memory sparse index for one SSTable.
type memIndex struct {
	key    []byte
	offset uint64 // byte offset within the data section
}

// Reader provides concurrent-safe point-get and sequential iteration.
// After Open returns, the file is only accessed via ReadAt (pread), so
// multiple goroutines may call Get or NewIterator concurrently.
type Reader struct {
	f       *os.File
	dataLen uint64
	index   []memIndex  // sparse index loaded at Open
	bf      *bloom.Filter // nil when no bloom section
}

// Open opens an existing SSTable for reading.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sstable open %s: %w", path, err)
	}
	r := &Reader{f: f}
	if err := r.load(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sstable load %s: %w", path, err)
	}
	return r, nil
}

// Close releases the file descriptor.
func (r *Reader) Close() error { return r.f.Close() }

// Get searches for key. Returns (value, kind, true, nil) when found.
// A bloom filter fast-reject returns (nil, 0, false, nil) immediately.
// Safe for concurrent use.
func (r *Reader) Get(key []byte) ([]byte, record.Kind, bool, error) {
	if r.bf != nil && !r.bf.MayContain(key) {
		return nil, 0, false, nil
	}
	startOff := r.searchIndex(key)
	return r.scanFrom(startOff, key)
}

// scanFrom reads data from startOff and scans forward until key is found or surpassed.
func (r *Reader) scanFrom(startOff uint64, key []byte) ([]byte, record.Kind, bool, error) {
	off := int64(startOff)
	for uint64(off) < r.dataLen {
		k, v, kind, n, err := readEntryAt(r.f, off)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, 0, false, fmt.Errorf("sstable scan: %w", err)
		}
		off += n
		cmp := bytes.Compare(k, key)
		if cmp == 0 {
			return v, kind, true, nil
		}
		if cmp > 0 {
			break
		}
	}
	return nil, 0, false, nil
}

// searchIndex returns the data-section offset of the last index entry whose
// key is ≤ target, or 0 if target precedes all entries.
func (r *Reader) searchIndex(key []byte) uint64 {
	off := uint64(0)
	for _, ie := range r.index {
		if bytes.Compare(ie.key, key) > 0 {
			break
		}
		off = ie.offset
	}
	return off
}

// load reads the footer, index section, and bloom section into memory.
func (r *Reader) load() error {
	info, err := r.f.Stat()
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if info.Size() < footerSize {
		return fmt.Errorf("file too small: %d bytes", info.Size())
	}

	foot := make([]byte, footerSize)
	if _, err := r.f.ReadAt(foot, info.Size()-footerSize); err != nil {
		return fmt.Errorf("read footer: %w", err)
	}
	if string(foot[40:48]) != string(magic[:]) {
		return fmt.Errorf("bad magic %q", foot[40:48])
	}

	r.dataLen = binary.LittleEndian.Uint64(foot[0:8])
	indexOff := binary.LittleEndian.Uint64(foot[8:16])
	indexLen := binary.LittleEndian.Uint64(foot[16:24])
	bloomOff := binary.LittleEndian.Uint64(foot[24:32])
	bloomLen := binary.LittleEndian.Uint64(foot[32:40])

	if indexLen > 0 {
		idxBuf := make([]byte, indexLen)
		if _, err := r.f.ReadAt(idxBuf, int64(indexOff)); err != nil {
			return fmt.Errorf("read index: %w", err)
		}
		r.index, err = parseIndex(idxBuf)
		if err != nil {
			return err
		}
	}

	if bloomLen > 0 {
		bfBuf := make([]byte, bloomLen)
		if _, err := r.f.ReadAt(bfBuf, int64(bloomOff)); err != nil {
			return fmt.Errorf("read bloom: %w", err)
		}
		r.bf, err = bloom.FromBytes(bfBuf)
		if err != nil {
			return fmt.Errorf("parse bloom: %w", err)
		}
	}
	return nil
}

func parseIndex(b []byte) ([]memIndex, error) {
	var idx []memIndex
	for len(b) > 0 {
		if len(b) < 4 {
			return nil, fmt.Errorf("sstable index: truncated at key-len")
		}
		keyLen := binary.LittleEndian.Uint32(b[0:4])
		b = b[4:]
		if uint32(len(b)) < keyLen+8 {
			return nil, fmt.Errorf("sstable index: truncated at offset")
		}
		key := make([]byte, keyLen)
		copy(key, b[:keyLen])
		b = b[keyLen:]
		offset := binary.LittleEndian.Uint64(b[0:8])
		b = b[8:]
		idx = append(idx, memIndex{key: key, offset: offset})
	}
	return idx, nil
}

// readEntryAt reads one data-section entry at the given file offset using
// ReadAt (pread). It does not affect the file's current seek position, making
// it safe to call concurrently from multiple goroutines.
//
// Returns key, value, kind, bytes-consumed, error.
func readEntryAt(f *os.File, off int64) (key, value []byte, kind record.Kind, n int64, err error) {
	var buf [4]byte

	// key_len
	if _, err = f.ReadAt(buf[:], off); err != nil {
		return
	}
	keyLen := int64(binary.LittleEndian.Uint32(buf[:]))
	off += 4
	n += 4

	// key
	key = make([]byte, keyLen)
	if _, err = f.ReadAt(key, off); err != nil {
		err = fmt.Errorf("read key: %w", err)
		return
	}
	off += keyLen
	n += keyLen

	// val_len
	if _, err = f.ReadAt(buf[:], off); err != nil {
		err = fmt.Errorf("read val-len: %w", err)
		return
	}
	valLen := int64(binary.LittleEndian.Uint32(buf[:]))
	off += 4
	n += 4

	// val
	value = make([]byte, valLen)
	if _, err = f.ReadAt(value, off); err != nil {
		err = fmt.Errorf("read val: %w", err)
		return
	}
	off += valLen
	n += valLen

	// kind
	var kb [1]byte
	if _, err = f.ReadAt(kb[:], off); err != nil {
		err = fmt.Errorf("read kind: %w", err)
		return
	}
	kind = record.Kind(kb[0])
	n++
	return
}

// Iterator streams all entries in ascending key order.
// An Iterator must NOT be shared across goroutines.
type Iterator struct {
	r      *Reader
	offset uint64 // current position within the data section
	key    []byte
	value  []byte
	kind   record.Kind
	err    error
}

// NewIterator returns an iterator positioned before the first entry.
func (r *Reader) NewIterator() *Iterator {
	return &Iterator{r: r}
}

// SeekGE positions the iterator so that the next Next call returns the first
// entry with key >= target. If target is nil or empty, seeks to the beginning.
func (it *Iterator) SeekGE(target []byte) {
	it.key = nil
	it.value = nil
	it.err = nil
	if len(target) == 0 {
		it.offset = 0
		return
	}
	it.offset = it.r.searchIndex(target)
	// Scan past any entries strictly less than target within this index block.
	for it.offset < it.r.dataLen {
		k, _, _, n, err := readEntryAt(it.r.f, int64(it.offset))
		if err != nil {
			if err == io.EOF {
				return
			}
			it.err = err
			return
		}
		if bytes.Compare(k, target) >= 0 {
			return // it.offset now points at the first entry >= target
		}
		it.offset += uint64(n)
	}
}

// Next advances to the next entry. Returns false when exhausted or on error.
func (it *Iterator) Next() bool {
	if it.offset >= it.r.dataLen {
		return false
	}
	k, v, kind, n, err := readEntryAt(it.r.f, int64(it.offset))
	if err != nil {
		if err == io.EOF {
			return false
		}
		it.err = err
		return false
	}
	it.offset += uint64(n)
	it.key = k
	it.value = v
	it.kind = kind
	return true
}

// Key returns the current key. Valid only after a successful Next call.
func (it *Iterator) Key() []byte { return it.key }

// Value returns the current value.
func (it *Iterator) Value() []byte { return it.value }

// Kind returns the current entry kind (value or tombstone).
func (it *Iterator) Kind() record.Kind { return it.kind }

// Err returns any error encountered during iteration.
func (it *Iterator) Err() error { return it.err }
