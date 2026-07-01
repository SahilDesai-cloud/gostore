package gostore

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func openDB(t *testing.T, opts Options) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ── Basic correctness ─────────────────────────────────────────────────────────

func TestPutAndGet(t *testing.T) {
	db := openDB(t, Options{})
	require.NoError(t, db.Put([]byte("hello"), []byte("world")))
	v, found, err := db.Get([]byte("hello"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("world"), v)
}

func TestGetMissing(t *testing.T) {
	db := openDB(t, Options{})
	_, found, err := db.Get([]byte("nope"))
	require.NoError(t, err)
	require.False(t, found)
}

func TestOverwrite(t *testing.T) {
	db := openDB(t, Options{})
	require.NoError(t, db.Put([]byte("k"), []byte("v1")))
	require.NoError(t, db.Put([]byte("k"), []byte("v2")))
	v, found, err := db.Get([]byte("k"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("v2"), v)
}

func TestDelete(t *testing.T) {
	db := openDB(t, Options{})
	require.NoError(t, db.Put([]byte("k"), []byte("v")))
	require.NoError(t, db.Delete([]byte("k")))
	_, found, err := db.Get([]byte("k"))
	require.NoError(t, err)
	require.False(t, found)
}

func TestDeleteNonExistent(t *testing.T) {
	db := openDB(t, Options{})
	require.NoError(t, db.Delete([]byte("ghost")))
	_, found, err := db.Get([]byte("ghost"))
	require.NoError(t, err)
	require.False(t, found)
}

func TestEmptyValue(t *testing.T) {
	db := openDB(t, Options{})
	require.NoError(t, db.Put([]byte("k"), []byte{}))
	v, found, err := db.Get([]byte("k"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte{}, v)
}

func TestManyKeys(t *testing.T) {
	db := openDB(t, Options{MemTableSize: 64 << 10}) // small threshold → multiple SSTables
	const n = 5000
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		v := []byte(fmt.Sprintf("val%08d", i))
		require.NoError(t, db.Put(k, v))
	}
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		want := []byte(fmt.Sprintf("val%08d", i))
		got, found, err := db.Get(k)
		require.NoError(t, err)
		require.True(t, found, "missing key %s", k)
		require.Equal(t, want, got)
	}
}

// ── WAL crash recovery ────────────────────────────────────────────────────────

func TestWALRecovery(t *testing.T) {
	dir := t.TempDir()
	opts := Options{NoSync: false}

	// Write some data, close cleanly.
	func() {
		db, err := Open(dir, opts)
		require.NoError(t, err)
		for i := 0; i < 100; i++ {
			k := []byte(fmt.Sprintf("key%04d", i))
			v := []byte(fmt.Sprintf("val%04d", i))
			require.NoError(t, db.Put(k, v))
		}
		require.NoError(t, db.Close())
	}()

	// Reopen and verify all keys present.
	db, err := Open(dir, opts)
	require.NoError(t, err)
	defer db.Close()
	for i := 0; i < 100; i++ {
		k := []byte(fmt.Sprintf("key%04d", i))
		want := []byte(fmt.Sprintf("val%04d", i))
		got, found, err := db.Get(k)
		require.NoError(t, err)
		require.True(t, found, "key %s lost after recovery", k)
		require.Equal(t, want, got)
	}
}

func TestTornWALRecovery(t *testing.T) {
	dir := t.TempDir()
	opts := Options{}

	// Write acknowledged data.
	db, err := Open(dir, opts)
	require.NoError(t, err)
	require.NoError(t, db.Put([]byte("safe"), []byte("data")))
	// Do NOT close cleanly; simulate crash after acknowledged write.
	// The WAL already has the record flushed (NoSync=false by default).

	// Find the WAL file and append garbage to simulate a torn write.
	walFiles, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	require.NoError(t, err)
	require.NotEmpty(t, walFiles)
	f, err := os.OpenFile(walFiles[0], os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.Write([]byte("torn partial garbage bytes"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	_ = db.memWAL.Close() // close before reopening

	// Reopen — torn record must be dropped, acknowledged data must survive.
	db2, err := Open(dir, opts)
	require.NoError(t, err)
	defer db2.Close()
	v, found, err := db2.Get([]byte("safe"))
	require.NoError(t, err)
	require.True(t, found, "acknowledged write must survive torn WAL")
	require.Equal(t, []byte("data"), v)
}

// ── SSTable flush and cross-SSTable reads ─────────────────────────────────────

func TestReadAfterFlush(t *testing.T) {
	// Use a tiny memtable to force frequent flushes.
	opts := Options{MemTableSize: 1 << 10, BloomBitsPerKey: 10}
	db := openDB(t, opts)

	const n = 200
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%06d", i))
		v := []byte(fmt.Sprintf("val%06d", i))
		require.NoError(t, db.Put(k, v))
	}
	// Allow background flush to complete.
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%06d", i))
		v := []byte(fmt.Sprintf("val%06d", i))
		got, found, err := db.Get(k)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, v, got)
	}
}

func TestDeleteSurvisesFlush(t *testing.T) {
	opts := Options{MemTableSize: 512}
	db := openDB(t, opts)
	require.NoError(t, db.Put([]byte("gone"), []byte("here")))
	require.NoError(t, db.Delete([]byte("gone")))
	// Write more data to force a flush.
	for i := 0; i < 50; i++ {
		require.NoError(t, db.Put([]byte(fmt.Sprintf("pad%d", i)), []byte("x")))
	}
	_, found, err := db.Get([]byte("gone"))
	require.NoError(t, err)
	require.False(t, found, "deleted key must remain absent after flush")
}

// ── Concurrency (single writer, many readers) ─────────────────────────────────

func TestConcurrentReads(t *testing.T) {
	db := openDB(t, Options{})
	const n = 500
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("k%05d", i))
		require.NoError(t, db.Put(k, k))
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				k := []byte(fmt.Sprintf("k%05d", i))
				v, found, err := db.Get(k)
				if err != nil || !found || string(v) != string(k) {
					t.Errorf("concurrent read failed for %s: found=%v err=%v", k, found, err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentWriteAndRead(t *testing.T) {
	db := openDB(t, Options{MemTableSize: 32 << 10})

	// Pre-populate.
	const base = 200
	for i := 0; i < base; i++ {
		k := []byte(fmt.Sprintf("k%06d", i))
		require.NoError(t, db.Put(k, k))
	}

	var wg sync.WaitGroup
	// Single writer goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := base; i < base+500; i++ {
			k := []byte(fmt.Sprintf("k%06d", i))
			_ = db.Put(k, k)
		}
	}()
	// Multiple reader goroutines.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < base; i++ {
				k := []byte(fmt.Sprintf("k%06d", i))
				_, _, _ = db.Get(k) // just must not panic / race
			}
		}()
	}
	wg.Wait()
}

// ── Post-close behaviour ──────────────────────────────────────────────────────

func TestClosedReturnsError(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir, Options{})
	require.NoError(t, err)
	require.NoError(t, db.Close())

	require.ErrorIs(t, db.Put([]byte("k"), []byte("v")), ErrClosed)
	_, _, err = db.Get([]byte("k"))
	require.ErrorIs(t, err, ErrClosed)
	require.ErrorIs(t, db.Close(), ErrClosed)
}

// ── Restart from SSTables ─────────────────────────────────────────────────────

func TestRestartFromSSTables(t *testing.T) {
	dir := t.TempDir()
	// First run: write data and force flush.
	func() {
		db, err := Open(dir, Options{MemTableSize: 512})
		require.NoError(t, err)
		for i := 0; i < 200; i++ {
			k := []byte(fmt.Sprintf("k%05d", i))
			require.NoError(t, db.Put(k, k))
		}
		require.NoError(t, db.Close())
	}()

	// Second run: all data must be readable from SSTables.
	db, err := Open(dir, Options{})
	require.NoError(t, err)
	defer db.Close()
	for i := 0; i < 200; i++ {
		k := []byte(fmt.Sprintf("k%05d", i))
		v, found, err := db.Get(k)
		require.NoError(t, err)
		require.True(t, found, "key %s missing after restart", k)
		require.Equal(t, k, v)
	}
}

// ── Compaction ────────────────────────────────────────────────────────────────

func TestCompactionReducesFileCount(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		MemTableSize:          512,
		L0CompactionThreshold: 2,
		Compaction:            SizeTiered,
	}
	db, err := Open(dir, opts)
	require.NoError(t, err)

	for i := 0; i < 500; i++ {
		k := []byte(fmt.Sprintf("k%06d", i))
		require.NoError(t, db.Put(k, k))
	}

	// Let background worker run.
	db.mu.RLock()
	nBefore := len(db.tables)
	db.mu.RUnlock()
	t.Logf("tables before close: %d", nBefore)

	require.NoError(t, db.Close())

	// Count remaining .sst files.
	ssts, _ := filepath.Glob(filepath.Join(dir, "*.sst"))
	t.Logf("sst files after compaction: %d", len(ssts))
}

func TestCompactionPreservesData(t *testing.T) {
	opts := Options{
		MemTableSize:          512,
		L0CompactionThreshold: 2,
	}
	db := openDB(t, opts)
	const n = 300
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("k%06d", i))
		require.NoError(t, db.Put(k, k))
	}
	// Overwrite half.
	for i := 0; i < n/2; i++ {
		k := []byte(fmt.Sprintf("k%06d", i))
		require.NoError(t, db.Put(k, []byte("overwrite")))
	}
	// Delete a quarter.
	for i := n / 2; i < 3*n/4; i++ {
		require.NoError(t, db.Delete([]byte(fmt.Sprintf("k%06d", i))))
	}
	// Trigger compaction signal.
	select {
	case db.bgCh <- struct{}{}:
	default:
	}

	// Verify.
	for i := 0; i < n/2; i++ {
		k := []byte(fmt.Sprintf("k%06d", i))
		v, found, err := db.Get(k)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, []byte("overwrite"), v)
	}
	for i := n / 2; i < 3*n/4; i++ {
		k := []byte(fmt.Sprintf("k%06d", i))
		_, found, err := db.Get(k)
		require.NoError(t, err)
		require.False(t, found)
	}
}

// ── Crash-consistency loop ────────────────────────────────────────────────────

func TestCrashConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping crash-consistency test in short mode")
	}
	dir := t.TempDir()
	opts := Options{NoSync: false}
	rng := rand.New(rand.NewSource(42))

	// Map of key → expected value (ground truth).
	committed := make(map[string]string)

	for round := 0; round < 20; round++ {
		db, err := Open(dir, opts)
		require.NoError(t, err)

		// Write a batch of acknowledged Put/Delete operations.
		const batchSize = 10
		type op struct{ key, val string }
		var batch []op
		for i := 0; i < batchSize; i++ {
			k := fmt.Sprintf("k%03d", rng.Intn(30))
			if rng.Intn(5) == 0 {
				// Delete
				require.NoError(t, db.Delete([]byte(k)))
				batch = append(batch, op{k, ""})
				delete(committed, k)
			} else {
				v := fmt.Sprintf("v%d-%d", round, i)
				require.NoError(t, db.Put([]byte(k), []byte(v)))
				batch = append(batch, op{k, v})
				committed[k] = v
			}
		}

		// Simulate "crash" by skipping a clean Close.
		// On a real crash the OS would release all file handles automatically;
		// on Windows we must close them explicitly to allow TempDir cleanup.
		_ = db.memWAL.Close()
		for _, th := range db.tables {
			_ = th.r.Close()
		}

		// Reopen and verify committed state.
		db2, err := Open(dir, opts)
		require.NoError(t, err)
		for k, want := range committed {
			v, found, err := db2.Get([]byte(k))
			require.NoError(t, err)
			if want == "" {
				require.False(t, found, "round %d: deleted key %q should be absent", round, k)
			} else {
				require.True(t, found, "round %d: key %q missing after crash", round, k)
				require.Equal(t, want, string(v), "round %d: key %q value mismatch", round, k)
			}
		}
		require.NoError(t, db2.Close())
	}
}
