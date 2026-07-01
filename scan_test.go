package gostore

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// collectScan drains an Iterator into a slice of [key, value] pairs.
func collectScan(t *testing.T, it *Iterator) [][2]string {
	t.Helper()
	defer it.Close()
	var out [][2]string
	for it.Next() {
		out = append(out, [2]string{string(it.Key()), string(it.Value())})
	}
	require.NoError(t, it.Err())
	return out
}

func TestScanAll(t *testing.T) {
	db := openDB(t, Options{})
	keys := []string{"banana", "apple", "cherry", "date", "elderberry"}
	for _, k := range keys {
		require.NoError(t, db.Put([]byte(k), []byte("v:"+k)))
	}

	it, err := db.Scan(nil, nil)
	require.NoError(t, err)
	got := collectScan(t, it)

	// Results must be in sorted order and contain all keys.
	require.Len(t, got, len(keys))
	for i := 1; i < len(got); i++ {
		require.Less(t, got[i-1][0], got[i][0], "scan must be sorted")
	}
	byKey := make(map[string]string, len(got))
	for _, pair := range got {
		byKey[pair[0]] = pair[1]
	}
	for _, k := range keys {
		require.Equal(t, "v:"+k, byKey[k])
	}
}

func TestScanRange(t *testing.T) {
	db := openDB(t, Options{})
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("key%02d", i)
		require.NoError(t, db.Put([]byte(k), []byte(k)))
	}

	// Scan [key03, key07) — should yield key03..key06.
	it, err := db.Scan([]byte("key03"), []byte("key07"))
	require.NoError(t, err)
	got := collectScan(t, it)

	require.Len(t, got, 4)
	require.Equal(t, "key03", got[0][0])
	require.Equal(t, "key04", got[1][0])
	require.Equal(t, "key05", got[2][0])
	require.Equal(t, "key06", got[3][0])
}

func TestScanStartOnly(t *testing.T) {
	db := openDB(t, Options{})
	for i := 0; i < 5; i++ {
		require.NoError(t, db.Put([]byte(fmt.Sprintf("k%d", i)), []byte("v")))
	}

	it, err := db.Scan([]byte("k3"), nil)
	require.NoError(t, err)
	got := collectScan(t, it)

	require.Len(t, got, 2) // k3, k4
	require.Equal(t, "k3", got[0][0])
	require.Equal(t, "k4", got[1][0])
}

func TestScanEndOnly(t *testing.T) {
	db := openDB(t, Options{})
	for i := 0; i < 5; i++ {
		require.NoError(t, db.Put([]byte(fmt.Sprintf("k%d", i)), []byte("v")))
	}

	it, err := db.Scan(nil, []byte("k3"))
	require.NoError(t, err)
	got := collectScan(t, it)

	require.Len(t, got, 3) // k0, k1, k2
	require.Equal(t, "k0", got[0][0])
	require.Equal(t, "k2", got[2][0])
}

func TestScanEmpty(t *testing.T) {
	db := openDB(t, Options{})

	it, err := db.Scan(nil, nil)
	require.NoError(t, err)
	got := collectScan(t, it)
	require.Empty(t, got)
}

func TestScanEmptyRange(t *testing.T) {
	db := openDB(t, Options{})
	require.NoError(t, db.Put([]byte("z"), []byte("v")))

	// [a, b) contains no keys.
	it, err := db.Scan([]byte("a"), []byte("b"))
	require.NoError(t, err)
	got := collectScan(t, it)
	require.Empty(t, got)
}

func TestScanExcludesDeletedKeys(t *testing.T) {
	db := openDB(t, Options{})
	require.NoError(t, db.Put([]byte("keep"), []byte("yes")))
	require.NoError(t, db.Put([]byte("gone"), []byte("no")))
	require.NoError(t, db.Delete([]byte("gone")))

	it, err := db.Scan(nil, nil)
	require.NoError(t, err)
	got := collectScan(t, it)

	require.Len(t, got, 1)
	require.Equal(t, "keep", got[0][0])
}

func TestScanSeesLatestOverwrite(t *testing.T) {
	db := openDB(t, Options{})
	require.NoError(t, db.Put([]byte("k"), []byte("v1")))
	require.NoError(t, db.Put([]byte("k"), []byte("v2")))

	it, err := db.Scan(nil, nil)
	require.NoError(t, err)
	got := collectScan(t, it)

	require.Len(t, got, 1)
	require.Equal(t, "v2", got[0][1])
}

func TestScanAcrossFlush(t *testing.T) {
	// Force flush by using a tiny memtable, then scan across SSTable + memtable.
	opts := Options{MemTableSize: 512, BloomBitsPerKey: 10}
	db := openDB(t, opts)

	const n = 100
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key%04d", i)
		require.NoError(t, db.Put([]byte(k), []byte(k)))
	}

	it, err := db.Scan(nil, nil)
	require.NoError(t, err)
	got := collectScan(t, it)

	require.Len(t, got, n)
	for i, pair := range got {
		want := fmt.Sprintf("key%04d", i)
		require.Equal(t, want, pair[0])
		require.Equal(t, want, pair[1])
	}
}

func TestScanDeleteSurvivedByFlush(t *testing.T) {
	opts := Options{MemTableSize: 256}
	db := openDB(t, opts)

	require.NoError(t, db.Put([]byte("gone"), []byte("here")))
	require.NoError(t, db.Delete([]byte("gone")))
	// Pad to force a flush so the tombstone lands in an SSTable.
	for i := 0; i < 30; i++ {
		require.NoError(t, db.Put([]byte(fmt.Sprintf("pad%02d", i)), []byte("x")))
	}

	it, err := db.Scan(nil, nil)
	require.NoError(t, err)
	got := collectScan(t, it)
	for _, pair := range got {
		require.NotEqual(t, "gone", pair[0], "deleted key must not appear in scan")
	}
}

func TestScanClosedDB(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir, Options{})
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = db.Scan(nil, nil)
	require.ErrorIs(t, err, ErrClosed)
}

func TestScanCloseReleasesRefs(t *testing.T) {
	opts := Options{MemTableSize: 256, L0CompactionThreshold: 2}
	db := openDB(t, opts)
	for i := 0; i < 50; i++ {
		require.NoError(t, db.Put([]byte(fmt.Sprintf("k%02d", i)), []byte("v")))
	}

	it, err := db.Scan(nil, nil)
	require.NoError(t, err)
	// Partially consume then close — must not leak.
	it.Next()
	it.Close()

	// Trigger compaction; if refs are leaked the old files would never be
	// deleted and the compacted SSTable would remain inaccessible.
	select {
	case db.bgCh <- struct{}{}:
	default:
	}
}
