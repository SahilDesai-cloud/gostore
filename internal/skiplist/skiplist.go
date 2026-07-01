// Package skiplist implements an ordered skip list used as the in-memory memtable.
//
// The skip list gives O(log n) expected time for insert and point-get, and
// O(n) in-order iteration. It is NOT safe for concurrent use; the caller
// (the DB layer) is responsible for external synchronisation.
package skiplist

import (
	"bytes"
	"math/rand"

	"github.com/SahilDesai-cloud/gostore/internal/record"
)

const (
	maxLevel  = 12  // max number of forward pointer levels
	branching = 4   // a new level is added with probability 1/branching
)

// Entry is a single memtable record.
type Entry struct {
	Key   []byte
	Value []byte
	Kind  record.Kind
}

type node struct {
	key   []byte
	value []byte
	kind  record.Kind
	next  []*node
}

// SkipList is an ordered, in-memory data structure used as the memtable.
// It is NOT safe for concurrent use; callers must synchronise externally.
type SkipList struct {
	head   *node
	level  int
	size   int // approximate key+value bytes
	length int // number of entries (including tombstones)
	rng    *rand.Rand
}

// New returns an empty SkipList.
func New() *SkipList {
	return &SkipList{
		head:  &node{next: make([]*node, maxLevel)},
		level: 1,
		rng:   rand.New(rand.NewSource(0)),
	}
}

func (sl *SkipList) randomLevel() int {
	h := 1
	for h < maxLevel && sl.rng.Intn(branching) == 0 {
		h++
	}
	return h
}

// Put inserts or updates key. Pass kind=KindTombstone to record a deletion.
func (sl *SkipList) Put(key, value []byte, kind record.Kind) {
	update := make([]*node, maxLevel)
	cur := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for cur.next[i] != nil && bytes.Compare(cur.next[i].key, key) < 0 {
			cur = cur.next[i]
		}
		update[i] = cur
	}

	// Update in place if key already exists.
	if nxt := cur.next[0]; nxt != nil && bytes.Equal(nxt.key, key) {
		sl.size -= len(nxt.key) + len(nxt.value)
		nxt.value = cloneBytes(value)
		nxt.kind = kind
		sl.size += len(nxt.key) + len(nxt.value)
		return
	}

	lv := sl.randomLevel()
	if lv > sl.level {
		for i := sl.level; i < lv; i++ {
			update[i] = sl.head
		}
		sl.level = lv
	}

	n := &node{
		key:   cloneBytes(key),
		value: cloneBytes(value),
		kind:  kind,
		next:  make([]*node, lv),
	}
	for i := 0; i < lv; i++ {
		n.next[i] = update[i].next[i]
		update[i].next[i] = n
	}
	sl.size += len(n.key) + len(n.value)
	sl.length++
}

// Get returns the Entry for key. Returns (zero, false) if not present.
func (sl *SkipList) Get(key []byte) (Entry, bool) {
	cur := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for cur.next[i] != nil && bytes.Compare(cur.next[i].key, key) < 0 {
			cur = cur.next[i]
		}
	}
	n := cur.next[0]
	if n != nil && bytes.Equal(n.key, key) {
		return Entry{Key: n.key, Value: n.value, Kind: n.kind}, true
	}
	return Entry{}, false
}

// Size returns the approximate memory usage in bytes (keys + values).
func (sl *SkipList) Size() int { return sl.size }

// Len returns the number of entries.
func (sl *SkipList) Len() int { return sl.length }

// Iterator iterates entries in ascending key order.
type Iterator struct {
	sl  *SkipList
	cur *node
}

// NewIterator returns an iterator positioned before the first entry.
func (sl *SkipList) NewIterator() *Iterator {
	return &Iterator{sl: sl, cur: sl.head}
}

// SeekGE positions the iterator so that the next Next call returns the first
// entry with key >= target. If target is nil or empty, seeks to the beginning.
func (it *Iterator) SeekGE(target []byte) {
	if len(target) == 0 {
		it.cur = it.sl.head
		return
	}
	cur := it.sl.head
	for i := it.sl.level - 1; i >= 0; i-- {
		for cur.next[i] != nil && bytes.Compare(cur.next[i].key, target) < 0 {
			cur = cur.next[i]
		}
	}
	it.cur = cur // cur.next[0] is the first entry >= target
}

// Next advances and returns the next entry. Returns (zero, false) when done.
func (it *Iterator) Next() (Entry, bool) {
	if it.cur.next[0] == nil {
		return Entry{}, false
	}
	it.cur = it.cur.next[0]
	return Entry{Key: it.cur.key, Value: it.cur.value, Kind: it.cur.kind}, true
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
