// Package wal implements a write-ahead log with CRC32 record integrity checks.
//
// On-disk record format (little-endian throughout):
//
//	┌──────────────────────────────────────────────────┐
//	│ RECORD HEADER (8 bytes)                          │
//	│   body_length  uint32  – byte length of body     │
//	│   crc32        uint32  – IEEE CRC of body        │
//	├──────────────────────────────────────────────────┤
//	│ RECORD BODY (body_length bytes)                  │
//	│   type         uint8   – RecordPut or RecordDel  │
//	│   key_len      uint32  – byte length of key      │
//	│   key          []byte                            │
//	│   val_len      uint32  – byte length of val      │  ← only for RecordPut
//	│   val          []byte                            │  ← only for RecordPut
//	└──────────────────────────────────────────────────┘
//
// Recovery truncates a torn final record (bad checksum or short read) so that
// the file always ends on a complete, verified record boundary.
package wal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// Record types stored in WAL entries.
const (
	RecordPut uint8 = 1
	RecordDel uint8 = 2
)

// WAL is an append-only write-ahead log.
type WAL struct {
	f    *os.File
	bw   *bufio.Writer
	path string
	sync bool
}

// Open opens or creates the WAL at path. When fsync is true every Append
// flushes OS buffers and syncs to disk before returning.
func Open(path string, fsync bool) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("wal open %s: %w", path, err)
	}
	return &WAL{
		f:    f,
		bw:   bufio.NewWriterSize(f, 256<<10),
		path: path,
		sync: fsync,
	}, nil
}

// Path returns the filesystem path of this WAL.
func (w *WAL) Path() string { return w.path }

// Append serialises and appends one record. For RecordPut, value must be set.
func (w *WAL) Append(typ uint8, key, value []byte) error {
	body := encodeBody(typ, key, value)
	csum := crc32.ChecksumIEEE(body)

	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(body)))
	binary.LittleEndian.PutUint32(hdr[4:8], csum)

	if _, err := w.bw.Write(hdr[:]); err != nil {
		return fmt.Errorf("wal write header: %w", err)
	}
	if _, err := w.bw.Write(body); err != nil {
		return fmt.Errorf("wal write body: %w", err)
	}
	if err := w.bw.Flush(); err != nil {
		return fmt.Errorf("wal flush: %w", err)
	}
	if w.sync {
		if err := w.f.Sync(); err != nil {
			return fmt.Errorf("wal sync: %w", err)
		}
	}
	return nil
}

// Sync flushes the write buffer and issues an fsync.
func (w *WAL) Sync() error {
	if err := w.bw.Flush(); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close flushes any buffered data and closes the underlying file.
func (w *WAL) Close() error {
	if err := w.bw.Flush(); err != nil {
		_ = w.f.Close()
		return fmt.Errorf("wal close flush: %w", err)
	}
	return w.f.Close()
}

// Record is one decoded WAL entry.
type Record struct {
	Type  uint8
	Key   []byte
	Value []byte // nil for RecordDel
}

// Replay reads all intact records from path, calling fn for each in order.
// A torn final record (short read or bad CRC32) is silently truncated.
// Returns nil when path does not exist (first open, no WAL yet).
func Replay(path string, fn func(Record) error) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("wal replay open: %w", err)
	}
	defer f.Close()

	var valid int64 // byte offset of the end of the last good record
	for {
		var hdr [8]byte
		n, rerr := io.ReadFull(f, hdr[:])
		if n == 0 && rerr == io.EOF {
			break // clean end-of-file
		}
		if rerr != nil {
			break // torn header — truncate here
		}

		bodyLen := binary.LittleEndian.Uint32(hdr[0:4])
		wantCRC := binary.LittleEndian.Uint32(hdr[4:8])

		body := make([]byte, bodyLen)
		if _, rerr = io.ReadFull(f, body); rerr != nil {
			break // torn body — truncate
		}
		if crc32.ChecksumIEEE(body) != wantCRC {
			break // checksum mismatch — torn write
		}

		rec, perr := parseBody(body)
		if perr != nil {
			break
		}
		if err2 := fn(rec); err2 != nil {
			return err2
		}
		valid += 8 + int64(bodyLen)
	}

	// Truncate any torn tail.
	if info, serr := f.Stat(); serr == nil && info.Size() != valid {
		if terr := f.Truncate(valid); terr != nil {
			return fmt.Errorf("wal truncate: %w", terr)
		}
	}
	return nil
}

// encodeBody serialises a record body.
func encodeBody(typ uint8, key, value []byte) []byte {
	sz := 1 + 4 + len(key)
	if typ == RecordPut {
		sz += 4 + len(value)
	}
	buf := make([]byte, 0, sz)
	buf = append(buf, typ)
	buf = appendU32(buf, uint32(len(key)))
	buf = append(buf, key...)
	if typ == RecordPut {
		buf = appendU32(buf, uint32(len(value)))
		buf = append(buf, value...)
	}
	return buf
}

// parseBody deserialises a record body produced by encodeBody.
func parseBody(body []byte) (Record, error) {
	if len(body) < 5 {
		return Record{}, fmt.Errorf("wal: body too short (%d bytes)", len(body))
	}
	typ := body[0]
	keyLen := binary.LittleEndian.Uint32(body[1:5])
	if uint32(len(body)) < 5+keyLen {
		return Record{}, fmt.Errorf("wal: body truncated at key")
	}
	key := make([]byte, keyLen)
	copy(key, body[5:5+keyLen])
	rest := body[5+keyLen:]

	var value []byte
	if typ == RecordPut {
		if len(rest) < 4 {
			return Record{}, fmt.Errorf("wal: body truncated at val-len")
		}
		valLen := binary.LittleEndian.Uint32(rest[:4])
		if uint32(len(rest)) < 4+valLen {
			return Record{}, fmt.Errorf("wal: body truncated at val")
		}
		value = make([]byte, valLen)
		copy(value, rest[4:4+valLen])
	}
	return Record{Type: typ, Key: key, Value: value}, nil
}

func appendU32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}
