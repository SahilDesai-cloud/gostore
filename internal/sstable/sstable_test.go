package sstable

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/user/gostore/internal/record"
)

func buildTable(t *testing.T, dir string, n int, bitsPerKey int) string {
	t.Helper()
	path := filepath.Join(dir, "test.sst")
	w, err := NewWriter(path, bitsPerKey)
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		v := []byte(fmt.Sprintf("val%08d", i))
		require.NoError(t, w.Add(k, v, record.KindValue))
	}
	require.NoError(t, w.Finish())
	return path
}

func TestWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	path := buildTable(t, dir, 1000, 10)

	r, err := Open(path)
	require.NoError(t, err)
	defer r.Close()

	// Verify every key can be retrieved.
	for i := 0; i < 1000; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		v := []byte(fmt.Sprintf("val%08d", i))
		got, kind, found, err := r.Get(k)
		require.NoError(t, err)
		require.True(t, found, "key %s must exist", k)
		require.Equal(t, record.KindValue, kind)
		require.Equal(t, v, got)
	}
}

func TestAbsentKey(t *testing.T) {
	dir := t.TempDir()
	path := buildTable(t, dir, 100, 10)
	r, err := Open(path)
	require.NoError(t, err)
	defer r.Close()

	_, _, found, err := r.Get([]byte("zzz_not_there"))
	require.NoError(t, err)
	require.False(t, found)
}

func TestTombstone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tomb.sst")
	w, err := NewWriter(path, 0)
	require.NoError(t, err)
	require.NoError(t, w.Add([]byte("a"), []byte("va"), record.KindValue))
	require.NoError(t, w.Add([]byte("b"), nil, record.KindTombstone))
	require.NoError(t, w.Add([]byte("c"), []byte("vc"), record.KindValue))
	require.NoError(t, w.Finish())

	r, err := Open(path)
	require.NoError(t, err)
	defer r.Close()

	_, kind, found, err := r.Get([]byte("b"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, record.KindTombstone, kind)
}

func TestIterator(t *testing.T) {
	dir := t.TempDir()
	const n = 500
	path := buildTable(t, dir, n, 10)

	r, err := Open(path)
	require.NoError(t, err)
	defer r.Close()

	it := r.NewIterator()
	count := 0
	prev := []byte{}
	for it.Next() {
		require.True(t, len(prev) == 0 || string(it.Key()) > string(prev),
			"iterator out of order at entry %d", count)
		prev = it.Key()
		count++
	}
	require.NoError(t, it.Err())
	require.Equal(t, n, count)
}

func TestNoBloomFilter(t *testing.T) {
	dir := t.TempDir()
	path := buildTable(t, dir, 200, 0) // bitsPerKey=0 → no bloom filter
	r, err := Open(path)
	require.NoError(t, err)
	defer r.Close()

	k := []byte(fmt.Sprintf("key%08d", 100))
	v := []byte(fmt.Sprintf("val%08d", 100))
	got, _, found, err := r.Get(k)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, v, got)
}

func TestSingleEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.sst")
	w, err := NewWriter(path, 10)
	require.NoError(t, err)
	require.NoError(t, w.Add([]byte("only"), []byte("value"), record.KindValue))
	require.NoError(t, w.Finish())

	r, err := Open(path)
	require.NoError(t, err)
	defer r.Close()

	v, kind, found, err := r.Get([]byte("only"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, record.KindValue, kind)
	require.Equal(t, []byte("value"), v)

	_, _, found, err = r.Get([]byte("missing"))
	require.NoError(t, err)
	require.False(t, found)
}

func BenchmarkWriterAdd(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.sst")
	w, err := NewWriter(path, 10)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		_ = w.Add(k, k, record.KindValue)
	}
	_ = w.Finish()
}
