package gostore

import (
	"bytes"
	"container/heap"
	"fmt"
	"os"

	"github.com/SahilDesai-cloud/gostore/internal/record"
	"github.com/SahilDesai-cloud/gostore/internal/sstable"
)

// needsCompaction reports whether background compaction should run.
func (db *DB) needsCompaction() bool {
	db.mu.RLock()
	n := len(db.tables)
	db.mu.RUnlock()
	return n >= db.opts.L0CompactionThreshold
}

// compact runs one compaction round using the configured strategy.
func (db *DB) compact() error {
	switch db.opts.Compaction {
	case Leveled:
		return db.leveledCompact()
	default:
		return db.sizeTieredCompact()
	}
}

// ── Size-tiered compaction ────────────────────────────────────────────────────

// sizeTieredCompact merges all current SSTables into one sorted SSTable.
// It is safe to call concurrently with reads and with flushes adding new tables.
func (db *DB) sizeTieredCompact() error {
	// ── 1. Snapshot the table list and take a compaction-snapshot reference ──
	db.mu.RLock()
	snapshot := make([]*tableHandle, len(db.tables))
	copy(snapshot, db.tables)
	for _, th := range snapshot {
		th.retain() // hold a ref so the file cannot be deleted under us
	}
	db.mu.RUnlock()

	// Always release the compaction-snapshot refs on exit.
	// The live-list refs are released separately (step 4) only on the success
	// path, so an early return here leaves the live list intact.
	defer func() {
		for _, th := range snapshot {
			th.release()
		}
	}()

	if len(snapshot) < db.opts.L0CompactionThreshold {
		return nil
	}

	// ── 2. Build the merged SSTable (no lock held) ───────────────────────────
	iters := make([]*sstable.Iterator, len(snapshot))
	for i, th := range snapshot {
		iters[i] = th.r.NewIterator()
	}

	seq := db.nextSeq.Add(1)
	outPath := db.sstPath(seq)
	w, err := sstable.NewWriter(outPath, db.opts.BloomBitsPerKey)
	if err != nil {
		return fmt.Errorf("compact: create: %w", err)
	}
	if err := mergeIterators(iters, w, true); err != nil {
		_ = w.Close()
		_ = os.Remove(outPath)
		return fmt.Errorf("compact: merge: %w", err)
	}
	if err := w.Finish(); err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("compact: finish: %w", err)
	}
	r, err := sstable.Open(outPath)
	if err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("compact: open result: %w", err)
	}
	newTH := newTableHandle(r, outPath)

	// ── 3. Atomically replace the compacted tables with the merged one ───────
	// Use pointer identity to remove exactly the snapshot tables, preserving
	// any tables added by concurrent flushes.
	db.mu.Lock()
	compacted := make(map[*tableHandle]bool, len(snapshot))
	for _, th := range snapshot {
		compacted[th] = true
	}
	newList := make([]*tableHandle, 0, len(db.tables)-len(snapshot)+1)
	newList = append(newList, newTH)
	for _, th := range db.tables {
		if !compacted[th] {
			newList = append(newList, th)
		}
	}
	db.tables = newList
	db.mu.Unlock()

	// ── 4. Release the live-list refs for each compacted table ───────────────
	// Combined with the deferred snapshot-ref release this brings each table's
	// ref count to zero (when no readers remain), triggering file deletion.
	for _, th := range snapshot {
		th.release()
	}
	return nil
}

// ── Leveled compaction ────────────────────────────────────────────────────────

// leveledCompact runs a simplified leveled strategy. True per-level file
// tracking via a manifest would be required for full LevelDB-style leveling;
// for now we delegate to size-tiered which produces a single merged file.
func (db *DB) leveledCompact() error {
	return db.sizeTieredCompact()
}

// ── k-way merge via min-heap ──────────────────────────────────────────────────

type mergeItem struct {
	key    []byte
	value  []byte
	kind   record.Kind
	age    int // lower = newer SSTable (tables[0] is newest)
	it     *sstable.Iterator
}

type mergeHeap []mergeItem

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	cmp := bytes.Compare(h[i].key, h[j].key)
	if cmp != 0 {
		return cmp < 0
	}
	return h[i].age < h[j].age // tie-break: prefer newer SSTable
}
func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x any)   { *h = append(*h, x.(mergeItem)) }
func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// mergeIterators performs a k-way sorted merge of iters into w.
//
// On duplicate keys the entry from the iterator with the lowest age (newest
// SSTable) wins; older versions are discarded. When dropTombstones is true,
// tombstone entries are dropped entirely (safe when compacting all files so
// no older version can be shadowed).
func mergeIterators(iters []*sstable.Iterator, w *sstable.Writer, dropTombstones bool) error {
	h := &mergeHeap{}
	heap.Init(h)

	for i, it := range iters {
		if it.Next() {
			heap.Push(h, mergeItem{
				key:   it.Key(),
				value: it.Value(),
				kind:  it.Kind(),
				age:   i,
				it:    it,
			})
		}
		if it.Err() != nil {
			return it.Err()
		}
	}

	var lastKey []byte
	for h.Len() > 0 {
		item := heap.Pop(h).(mergeItem)

		// Re-seed the heap from this iterator.
		if item.it.Next() {
			heap.Push(h, mergeItem{
				key:   item.it.Key(),
				value: item.it.Value(),
				kind:  item.it.Kind(),
				age:   item.age,
				it:    item.it,
			})
		}
		if item.it.Err() != nil {
			return item.it.Err()
		}

		// Drop older versions of the same key.
		if lastKey != nil && bytes.Equal(item.key, lastKey) {
			continue
		}
		lastKey = item.key

		if dropTombstones && item.kind == record.KindTombstone {
			continue
		}

		if err := w.Add(item.key, item.value, item.kind); err != nil {
			return err
		}
	}
	return nil
}
