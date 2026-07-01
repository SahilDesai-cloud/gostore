# gostore

A persistent, crash-safe key-value store built from scratch on a **Log-Structured Merge (LSM) tree** in Go. Implemented as a single-node embeddable library — no network layer, no CGo, no external dependencies beyond the standard library (plus `testify` for tests).

## Features

- **Crash-safe writes** — every write is appended to a Write-Ahead Log (WAL) with a CRC32 checksum; torn records are truncated on recovery
- **Ordered storage** — keys are kept sorted in SSTables; range scans are O(log N) to seek then O(k) to iterate
- **Bloom filters** — Kirsch-Mitzenmacher double-hashing rejects absent keys in O(1) without touching disk
- **Background compaction** — size-tiered k-way merge reclaims space and reduces read amplification
- **Concurrent reads** — `ReadAt` (pread) lets many goroutines query SSTables with no lock held
- **Reference-counted SSTables** — compaction never deletes a file while a reader holds a reference

## Architecture

```
┌───────────────────────────────────────────────────────────┐
│  Put / Delete / Get / Scan (public API)                   │
└──────────────────┬──────────────┬─────────────────────────┘
                   │ write        │ read
          ┌────────▼────────┐     │
          │  Write-Ahead    │     │
          │  Log (WAL)      │     │
          └────────┬────────┘     │
                   │              │
          ┌────────▼────────┐     │      ┌───────────────────┐
          │  Memtable       ◄─────┘      │  Immutable        │
          │  (skip list)    │  rotate    │  Memtables (imm)  │
          └────────┬────────┘ ─────────► └────────┬──────────┘
                   │                              │ flush
                   │                    ┌─────────▼──────────┐
                   │                    │  SSTables on disk   │
                   │                    │  ┌───────────────┐  │
                   │                    │  │ data blocks   │  │
                   │                    │  │ sparse index  │  │
                   │                    │  │ bloom filter  │  │
                   │                    │  └───────────────┘  │
                   │                    └─────────┬──────────┘
                   └──────────────────────────────┘
                                compaction
```

### Read path

`Get` and `Scan` check layers newest-first:

1. Active memtable (skip list, O(log N))
2. Immutable memtables, newest-first (O(log N) each)
3. SSTables, newest-first — bloom filter fast-reject → sparse index seek → data scan

### Write path

1. Append record to WAL (fsync optional)
2. Insert into active memtable
3. When memtable exceeds `MemTableSize`, rotate: freeze → `imm` queue → signal background worker
4. Background worker flushes `imm` entries to SSTables, then compacts when `L0CompactionThreshold` is reached

### Crash recovery

On `Open`, the engine replays any un-flushed WAL files in ascending sequence order into a fresh memtable. Torn records (partial writes at the tail) are detected via CRC32 and truncated before replay.

## API

```go
// Open opens or creates the database at dir.
db, err := gostore.Open(dir, gostore.Options{})

// Put stores value under key.
err = db.Put([]byte("hello"), []byte("world"))

// Get retrieves the value for key.
// Returns (nil, false, nil) when the key is absent or deleted.
value, found, err := db.Get([]byte("hello"))

// Delete writes a tombstone; the key disappears from all future reads.
err = db.Delete([]byte("hello"))

// Scan returns an ordered iterator over [start, end).
// Either bound may be nil for unbounded.
it, err := db.Scan([]byte("a"), []byte("z"))
defer it.Close()
for it.Next() {
    fmt.Printf("%s = %s\n", it.Key(), it.Value())
}
if err := it.Err(); err != nil { ... }

// Close flushes all in-memory data and releases resources.
err = db.Close()
```

## Options

```go
type Options struct {
    // MemTableSize is the byte threshold that triggers a memtable rotation.
    // Default: 4 MiB.
    MemTableSize int64

    // BloomBitsPerKey controls bloom filter density (bits per key).
    // 0 disables bloom filters. Default: 10 (~1% false-positive rate).
    BloomBitsPerKey int

    // Compaction selects the compaction strategy (SizeTiered or Leveled).
    // Default: SizeTiered.
    Compaction CompactionStrategy

    // L0CompactionThreshold is the SSTable count that triggers compaction.
    // Default: 4.
    L0CompactionThreshold int

    // NoSync skips fsync on WAL writes. Improves write throughput at the
    // cost of durability on power loss. Default: false.
    NoSync bool
}
```

## Benchmarks

Measured on Windows, Intel Core Ultra 5 125H. `NoSync: true` for write benchmarks unless noted.

### Writes

| Benchmark | ops/s | latency | note |
|-----------|------:|--------:|------|
| PutSequential | 878,028 | 3.9 µs | WAL append, no fsync |
| PutRandom | 581,107 | 10.6 µs | random key order |
| PutFsync | 7,920 | 428 µs | fsync per write (durable) |

### Reads

| Benchmark | ops/s | latency | note |
|-----------|------:|--------:|------|
| GetHot | 12,856,857 | 248 ns | keys in active memtable |
| GetCold | 5,336 | 651 µs | keys spread across many SSTables |
| GetAbsent (bloom on) | 9,821,185 | 349 ns | absent key, 10 bits/key bloom |
| GetAbsent (bloom off) | 9,900,217 | 350 ns | absent key, no bloom |

### Amplification

| Metric | Result | Note |
|--------|-------:|------|
| Space amplification | **2.16×** | before vs after compacting a fully-overwritten dataset |
| Read amplification | ~94 SST files | 3,000 keys, 2 KB memtable, compaction disabled |
| WAL recovery | ~41 ms | 5,000 records replayed from WAL on restart |

### MemTable size vs write throughput

| MemTable size | ops/s | latency |
|---------------|------:|--------:|
| 64 KB | 161,966 | 28.2 µs |
| 512 KB | 779,451 | 13.7 µs |
| 4 MB | 769,762 | 12.6 µs |

## Project structure

```
gostore/
├── db.go               # DB type: Open, Put, Get, Delete, Close, WAL rotation, flush
├── iterator.go         # DB.Scan, merged Iterator (k-way heap across all layers)
├── compaction.go       # Size-tiered compaction, k-way SSTable merge
├── options.go          # Options with defaults
├── db_test.go          # 19 correctness and concurrency tests
├── scan_test.go        # 12 range-scan tests
├── bench_test.go       # Write, read, amplification, and recovery benchmarks
├── Makefile
├── .github/workflows/ci.yml   # Ubuntu + macOS + Windows; test + race + bench
└── internal/
    ├── record/         # Shared KindValue / KindTombstone constants
    ├── skiplist/       # Ordered skip list — O(log N) put/get, O(N) scan, SeekGE
    ├── wal/            # Write-Ahead Log — CRC32 records, torn-tail truncation on recovery
    ├── bloom/          # Bloom filter — Kirsch-Mitzenmacher double-hashing
    └── sstable/        # SSTable writer + reader — sparse index, bloom section, pread
```

## On-disk format

### WAL record

```
[ body_len: u32LE ][ crc32: u32LE ][ type: u8 ][ key_len: u32LE ][ key ][ val_len: u32LE ][ val ]
```

`crc32` covers the body only. `val_len` and `val` are omitted for Delete records.

### SSTable layout

```
[ data section:  key_len(u32) key val_len(u32) val kind(u8) … ]
[ index section: key_len(u32) key offset(u64) … ]  ← sparse, every 128th key
[ bloom section: k(u64) m(u64) bits([]u64) ]
[ footer (48 B): data_len(u64) index_off(u64) index_len(u64) bloom_off(u64) bloom_len(u64) magic[8] ]
```

Footer is always the last 48 bytes. Magic bytes: `GOSTORE1`.

## Running

```bash
# Build
make build

# Tests (all packages)
make test

# Benchmarks
make bench

# Race detector (requires CGo / GCC)
make race

# Crash-consistency test only
make crash-test
```
