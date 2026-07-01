package wal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	w, err := Open(path, true)
	require.NoError(t, err)

	require.NoError(t, w.Append(RecordPut, []byte("key1"), []byte("val1")))
	require.NoError(t, w.Append(RecordPut, []byte("key2"), []byte("val2")))
	require.NoError(t, w.Append(RecordDel, []byte("key3"), nil))
	require.NoError(t, w.Close())

	var recs []Record
	require.NoError(t, Replay(path, func(r Record) error {
		recs = append(recs, r)
		return nil
	}))
	require.Len(t, recs, 3)
	require.Equal(t, RecordPut, recs[0].Type)
	require.Equal(t, "key1", string(recs[0].Key))
	require.Equal(t, "val1", string(recs[0].Value))
	require.Equal(t, RecordDel, recs[2].Type)
	require.Nil(t, recs[2].Value)
}

func TestTornHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "torn.wal")

	// Write one good record.
	w, err := Open(path, true)
	require.NoError(t, err)
	require.NoError(t, w.Append(RecordPut, []byte("good"), []byte("data")))
	require.NoError(t, w.Close())

	goodSize := fileSize(t, path)

	// Append a partial header (torn write simulation).
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.Write([]byte{0x01, 0x02, 0x03}) // incomplete header
	require.NoError(t, err)
	require.NoError(t, f.Close())

	var recs []Record
	require.NoError(t, Replay(path, func(r Record) error {
		recs = append(recs, r)
		return nil
	}))
	require.Len(t, recs, 1, "torn partial header must be truncated")
	require.Equal(t, goodSize, fileSize(t, path), "file must be truncated to last good record")
}

func TestTornBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "torn2.wal")

	w, err := Open(path, true)
	require.NoError(t, err)
	require.NoError(t, w.Append(RecordPut, []byte("k"), []byte("v")))
	require.NoError(t, w.Close())

	goodSize := fileSize(t, path)

	// Append a valid-looking header but with no body.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	// Write a header claiming body of 100 bytes but provide none.
	hdr := []byte{100, 0, 0, 0, 0xDE, 0xAD, 0xBE, 0xEF}
	_, err = f.Write(hdr)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	var recs []Record
	require.NoError(t, Replay(path, func(r Record) error {
		recs = append(recs, r)
		return nil
	}))
	require.Len(t, recs, 1, "record with missing body must be dropped")
	require.Equal(t, goodSize, fileSize(t, path))
}

func TestBadChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.wal")

	w, err := Open(path, true)
	require.NoError(t, err)
	require.NoError(t, w.Append(RecordPut, []byte("k"), []byte("v")))
	require.NoError(t, w.Close())

	// Corrupt a byte inside the body (after the header).
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte{0xFF}, 9) // flip a byte in the body
	require.NoError(t, err)
	require.NoError(t, f.Close())

	var recs []Record
	require.NoError(t, Replay(path, func(r Record) error {
		recs = append(recs, r)
		return nil
	}))
	require.Empty(t, recs, "record with bad CRC must be dropped")
	require.Equal(t, int64(0), fileSize(t, path))
}

func TestNotExist(t *testing.T) {
	require.NoError(t, Replay("/nonexistent/path/wal.log", func(Record) error { return nil }))
}

func TestEmptyWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wal")
	w, err := Open(path, false)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	var recs []Record
	require.NoError(t, Replay(path, func(r Record) error {
		recs = append(recs, r)
		return nil
	}))
	require.Empty(t, recs)
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Size()
}
