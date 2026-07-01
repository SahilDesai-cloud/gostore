package gostore

import (
	"bytes"
	"container/heap"

	"github.com/user/gostore/internal/record"
	"github.com/user/gostore/internal/skiplist"
	"github.com/user/gostore/internal/sstable"
)

// Iterator is a merged, deduped, tombstone-filtered ordered view of the
// database at the moment Scan was called. It spans the active memtable,
// immutable memtables, and all SSTables. Entries are yielded in ascending
// key order.
//
// An Iterator is not safe for concurrent use. Call Close when done.
type Iterator struct {
	h      *scanHeap
	end    []byte // exclusive upper bound; nil = unbounded
	tables []*tableHandle
	key    []byte
	value  []byte
	err    error
	last   []byte // last key yielded, used to deduplicate across layers
}

// Next advances to the next live entry. Returns false when exhausted or on
// error; check Err afterwards.
func (it *Iterator) Next() bool {
	for it.h.Len() > 0 {
		item := heap.Pop(it.h).(scanItem)

		// Advance the source and re-seed the heap.
		if item.it.next() {
			heap.Push(it.h, scanItem{
				key:   item.it.key(),
				value: item.it.value(),
				kind:  item.it.kind(),
				age:   item.age,
				it:    item.it,
			})
		} else if item.it.err() != nil {
			it.err = item.it.err()
			return false
		}

		// Drop older versions of the same key (lower age = newer layer wins).
		if it.last != nil && bytes.Equal(item.key, it.last) {
			continue
		}

		// Check exclusive upper bound.
		if it.end != nil && bytes.Compare(item.key, it.end) >= 0 {
			return false
		}

		it.last = item.key

		// Tombstones mark deleted keys; skip them.
		if item.kind == record.KindTombstone {
			continue
		}

		it.key = item.key
		it.value = item.value
		return true
	}
	return false
}

// Key returns the current key. Valid only after a successful Next call.
func (it *Iterator) Key() []byte { return it.key }

// Value returns the current value. Valid only after a successful Next call.
func (it *Iterator) Value() []byte { return it.value }

// Err returns any error that stopped iteration early.
func (it *Iterator) Err() error { return it.err }

// Close releases the SSTable references held by the iterator. It must be
// called exactly once when the caller is done with the iterator.
func (it *Iterator) Close() {
	for _, th := range it.tables {
		th.release()
	}
	it.tables = nil
}

// Scan returns an Iterator that yields all live keys in [start, end) in
// ascending order. Either bound may be nil to indicate no bound:
//
//	db.Scan(nil, nil)           // all keys
//	db.Scan([]byte("a"), nil)   // "a" onwards
//	db.Scan(nil, []byte("m"))   // up to but not including "m"
//
// The caller must call Close on the returned Iterator when done to release
// internal SSTable references.
func (db *DB) Scan(start, end []byte) (*Iterator, error) {
	if db.closed.Load() {
		return nil, ErrClosed
	}

	// Snapshot all layers under the read lock and retain SSTable refs so
	// compaction cannot delete files while we hold the iterator.
	db.mu.RLock()
	memSnap := db.mem
	immSnap := make([]immEntry, len(db.imm))
	copy(immSnap, db.imm)
	tableSnap := make([]*tableHandle, len(db.tables))
	copy(tableSnap, db.tables)
	for _, th := range tableSnap {
		th.retain()
	}
	db.mu.RUnlock()

	// Build one layerIter per source. age 0 = newest (active memtable);
	// higher age = older. The heap uses age to break ties on equal keys so
	// the newest version always wins.
	age := 0
	type source struct {
		it  layerIter
		age int
	}
	var sources []source

	// Active memtable (newest).
	{
		sl := memSnap.NewIterator()
		sl.SeekGE(start)
		sources = append(sources, source{&slIter{it: sl}, age})
		age++
	}

	// Immutable memtables: db.imm[last] is most recently frozen.
	for i := len(immSnap) - 1; i >= 0; i-- {
		sl := immSnap[i].sl.NewIterator()
		sl.SeekGE(start)
		sources = append(sources, source{&slIter{it: sl}, age})
		age++
	}

	// SSTables: db.tables[0] is newest.
	for _, th := range tableSnap {
		si := th.r.NewIterator()
		si.SeekGE(start)
		sources = append(sources, source{&sstIter{it: si}, age})
		age++
	}

	// Prime the heap by consuming the first entry from each source.
	h := &scanHeap{}
	heap.Init(h)
	for _, src := range sources {
		if src.it.next() {
			heap.Push(h, scanItem{
				key:   src.it.key(),
				value: src.it.value(),
				kind:  src.it.kind(),
				age:   src.age,
				it:    src.it,
			})
		} else if src.it.err() != nil {
			for _, th := range tableSnap {
				th.release()
			}
			return nil, src.it.err()
		}
	}

	return &Iterator{
		h:      h,
		end:    end,
		tables: tableSnap,
	}, nil
}

// ── Internal merge infrastructure ─────────────────────────────────────────────

// layerIter is the common internal interface over memtable and SSTable sources.
type layerIter interface {
	next() bool
	key() []byte
	value() []byte
	kind() record.Kind
	err() error
}

// slIter adapts a skiplist.Iterator to layerIter.
type slIter struct {
	it  *skiplist.Iterator
	ent skiplist.Entry
}

func (s *slIter) next() bool {
	e, ok := s.it.Next()
	if ok {
		s.ent = e
	}
	return ok
}
func (s *slIter) key() []byte       { return s.ent.Key }
func (s *slIter) value() []byte     { return s.ent.Value }
func (s *slIter) kind() record.Kind { return s.ent.Kind }
func (s *slIter) err() error        { return nil }

// sstIter adapts an sstable.Iterator to layerIter.
type sstIter struct {
	it *sstable.Iterator
}

func (s *sstIter) next() bool        { return s.it.Next() }
func (s *sstIter) key() []byte       { return s.it.Key() }
func (s *sstIter) value() []byte     { return s.it.Value() }
func (s *sstIter) kind() record.Kind { return s.it.Kind() }
func (s *sstIter) err() error        { return s.it.Err() }

// scanItem is one element in the scanHeap.
type scanItem struct {
	key   []byte
	value []byte
	kind  record.Kind
	age   int // lower = newer layer
	it    layerIter
}

// scanHeap is a min-heap ordered by (key ASC, age ASC).
type scanHeap []scanItem

func (h scanHeap) Len() int { return len(h) }
func (h scanHeap) Less(i, j int) bool {
	if c := bytes.Compare(h[i].key, h[j].key); c != 0 {
		return c < 0
	}
	return h[i].age < h[j].age
}
func (h scanHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *scanHeap) Push(x any)   { *h = append(*h, x.(scanItem)) }
func (h *scanHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
