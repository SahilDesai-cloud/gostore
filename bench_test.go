package gostore

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// ── Write benchmarks ──────────────────────────────────────────────────────────

// BenchmarkPutSequential measures sequential Put throughput with fsync disabled.
func BenchmarkPutSequential(b *testing.B) {
	dir := b.TempDir()
	db, err := Open(dir, Options{NoSync: true, MemTableSize: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%012d", i))
		v := []byte(fmt.Sprintf("val%012d", i))
		if err := db.Put(k, v); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(24)
}

// BenchmarkPutRandom measures random-order Put throughput.
func BenchmarkPutRandom(b *testing.B) {
	dir := b.TempDir()
	db, err := Open(dir, Options{NoSync: true, MemTableSize: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	rng := rand.New(rand.NewSource(42))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%012d", rng.Int63()))
		v := []byte(fmt.Sprintf("val%012d", i))
		if err := db.Put(k, v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPutFsync measures durable Put throughput (fsync per write).
func BenchmarkPutFsync(b *testing.B) {
	dir := b.TempDir()
	db, err := Open(dir, Options{NoSync: false, MemTableSize: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%012d", i))
		if err := db.Put(k, []byte("v")); err != nil {
			b.Fatal(err)
		}
	}
}

// ── Read benchmarks ───────────────────────────────────────────────────────────

// BenchmarkGetHot measures reads from the active memtable (all keys fit in memory).
func BenchmarkGetHot(b *testing.B) {
	dir := b.TempDir()
	db, err := Open(dir, Options{NoSync: true})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	const n = 1000
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%06d", i))
		_ = db.Put(k, k)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%06d", i%n))
		if _, _, err := db.Get(k); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetCold measures reads spread across many SSTables.
func BenchmarkGetCold(b *testing.B) {
	dir := b.TempDir()
	db, err := Open(dir, Options{NoSync: true, MemTableSize: 4 << 10})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	const n = 5000
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		_ = db.Put(k, k)
	}
	b.ResetTimer()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%08d", rng.Intn(n)))
		if _, _, err := db.Get(k); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetAbsentBloomOn measures absent-key reads with bloom filters enabled.
func BenchmarkGetAbsentBloomOn(b *testing.B) {
	dir := b.TempDir()
	db, err := Open(dir, Options{NoSync: true, BloomBitsPerKey: 10, MemTableSize: 4 << 10})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 2000; i++ {
		_ = db.Put([]byte(fmt.Sprintf("present%08d", i)), []byte("v"))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = db.Get([]byte(fmt.Sprintf("absent%08d", i)))
	}
}

// BenchmarkGetAbsentBloomOff shows the cost of absent-key reads without bloom filters.
func BenchmarkGetAbsentBloomOff(b *testing.B) {
	dir := b.TempDir()
	db, err := Open(dir, Options{NoSync: true, BloomBitsPerKey: 0, MemTableSize: 4 << 10})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 2000; i++ {
		_ = db.Put([]byte(fmt.Sprintf("present%08d", i)), []byte("v"))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = db.Get([]byte(fmt.Sprintf("absent%08d", i)))
	}
}

// ── Amplification benchmarks ──────────────────────────────────────────────────

// BenchmarkReadAmplification reports SSTable file count alongside read throughput.
func BenchmarkReadAmplification(b *testing.B) {
	dir := b.TempDir()
	db, err := Open(dir, Options{
		NoSync:                true,
		MemTableSize:          2 << 10,
		L0CompactionThreshold: 1000, // prevent auto-compaction
	})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	const n = 3000
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		_ = db.Put(k, k)
	}
	db.mu.RLock()
	numTables := len(db.tables)
	db.mu.RUnlock()
	b.ReportMetric(float64(numTables), "sst-files")

	b.ResetTimer()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%08d", rng.Intn(n)))
		if _, _, err := db.Get(k); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSpaceAmplification measures on-disk byte reduction after compaction.
func BenchmarkSpaceAmplification(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := b.TempDir()
		db, err := Open(dir, Options{
			NoSync:                true,
			MemTableSize:          1 << 10,
			L0CompactionThreshold: 1000,
		})
		if err != nil {
			b.Fatal(err)
		}
		const n = 300
		for j := 0; j < n; j++ {
			k := []byte(fmt.Sprintf("key%06d", j))
			_ = db.Put(k, []byte("original_value"))
		}
		for j := 0; j < n; j++ {
			k := []byte(fmt.Sprintf("key%06d", j))
			_ = db.Put(k, []byte("updated_value_"))
		}
		_ = db.Close()
		sizeBefore := calcDirSize(dir)
		b.StartTimer()

		db2, err := Open(dir, Options{NoSync: true, MemTableSize: 1 << 10, L0CompactionThreshold: 1})
		if err != nil {
			b.Fatal(err)
		}
		_ = db2.compact()
		_ = db2.Close()

		b.StopTimer()
		sizeAfter := calcDirSize(dir)
		if sizeAfter > 0 {
			b.ReportMetric(float64(sizeBefore)/float64(sizeAfter), "space-amp-ratio")
		}
		b.StartTimer()
	}
}

// BenchmarkRecoveryTime measures crash-recovery speed (WAL replay).
func BenchmarkRecoveryTime(b *testing.B) {
	const walRecords = 5000
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := b.TempDir()
		// Build WAL without a clean close (simulate crash).
		db, err := Open(dir, Options{NoSync: true, MemTableSize: 256 << 20})
		if err != nil {
			b.Fatal(err)
		}
		for j := 0; j < walRecords; j++ {
			k := []byte(fmt.Sprintf("key%08d", j))
			_ = db.Put(k, k)
		}
		_ = db.memWAL.Sync() // ensure data on disk
		// Close WAL fd without flushing memtable → WAL remains on disk.
		_ = db.memWAL.Close()
		b.StartTimer()

		db2, err := Open(dir, Options{NoSync: true})
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		_ = db2.Close()
		b.StartTimer()
	}
	b.ReportMetric(walRecords, "wal-records")
}

// ── Sizing variants ───────────────────────────────────────────────────────────

func BenchmarkMemTableSizeTiered(b *testing.B) {
	for _, size := range []int64{64 << 10, 512 << 10, 4 << 20} {
		size := size
		b.Run(fmt.Sprintf("memtable-%dK", size>>10), func(b *testing.B) {
			dir := b.TempDir()
			db, err := Open(dir, Options{NoSync: true, MemTableSize: size, Compaction: SizeTiered})
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				k := []byte(fmt.Sprintf("key%012d", i))
				_ = db.Put(k, k)
			}
		})
	}
}

func calcDirSize(dir string) int64 {
	var total int64
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err == nil {
			total += info.Size()
		}
	}
	return total
}
