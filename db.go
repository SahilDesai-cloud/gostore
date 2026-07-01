package gostore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/SahilDesai-cloud/gostore/internal/record"
	"github.com/SahilDesai-cloud/gostore/internal/skiplist"
	"github.com/SahilDesai-cloud/gostore/internal/sstable"
	"github.com/SahilDesai-cloud/gostore/internal/wal"
)

// ErrClosed is returned when an operation is attempted on a closed DB.
var ErrClosed = errors.New("gostore: db is closed")

// DB is a persistent, crash-safe key-value store. It is safe for concurrent
// use: arbitrarily many readers with one writer at a time.
type DB struct {
	dir  string
	opts Options

	// mu guards mem, memSeq, memWAL, imm, and tables.
	mu     sync.RWMutex
	mem    *skiplist.SkipList
	memSeq uint64
	memWAL *wal.WAL

	imm    []immEntry     // frozen memtables waiting to be flushed; oldest first
	tables []*tableHandle // open SSTables; index 0 = newest

	nextSeq atomic.Uint64

	bgCh    chan struct{} // poke the background goroutine
	closeCh chan struct{} // closed when Close is called
	closeWg sync.WaitGroup
	closed  atomic.Bool
}

// immEntry is a frozen memtable together with the path of the WAL that built it.
// The WAL file is already closed (flushed) by the time it appears here.
type immEntry struct {
	sl      *skiplist.SkipList
	walPath string
	seq     uint64
}

// tableHandle wraps a Reader with an atomic reference count.
// The file is closed and deleted when refs reaches zero.
type tableHandle struct {
	r    *sstable.Reader
	path string
	refs atomic.Int32
}

func newTableHandle(r *sstable.Reader, path string) *tableHandle {
	h := &tableHandle{r: r, path: path}
	h.refs.Store(1) // the live-table list holds one reference
	return h
}

func (h *tableHandle) retain() { h.refs.Add(1) }

func (h *tableHandle) release() {
	if h.refs.Add(-1) == 0 {
		_ = h.r.Close()
		_ = os.Remove(h.path)
	}
}

// Open opens or creates the database in dir.
func Open(dir string, opts Options) (*DB, error) {
	opts.setDefaults()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("gostore open: mkdir: %w", err)
	}
	db := &DB{
		dir:     dir,
		opts:    opts,
		bgCh:    make(chan struct{}, 1),
		closeCh: make(chan struct{}),
	}
	db.mem = skiplist.New()
	if err := db.recover(); err != nil {
		return nil, fmt.Errorf("gostore open: %w", err)
	}
	db.closeWg.Add(1)
	go db.backgroundWorker()
	return db, nil
}

// Put stores value under key.
func (db *DB) Put(key, value []byte) error {
	return db.write(wal.RecordPut, key, value)
}

// Delete writes a tombstone for key. The key remains visible (as absent) until
// compaction removes both the tombstone and any older live entries.
func (db *DB) Delete(key []byte) error {
	return db.write(wal.RecordDel, key, nil)
}

func (db *DB) write(typ uint8, key, value []byte) error {
	if db.closed.Load() {
		return ErrClosed
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.memWAL.Append(typ, key, value); err != nil {
		return fmt.Errorf("gostore write wal: %w", err)
	}
	kind := record.KindValue
	if typ == wal.RecordDel {
		kind = record.KindTombstone
	}
	db.mem.Put(key, value, kind)

	if db.mem.Size() >= int(db.opts.MemTableSize) {
		db.rotateMemTable()
	}
	return nil
}

// rotateMemTable freezes the active memtable and opens a fresh one.
// Must be called with db.mu held (write lock).
func (db *DB) rotateMemTable() {
	oldWAL := db.memWAL
	// Flush OS buffer to disk so the WAL is safe before we stop writing to it.
	_ = oldWAL.Sync()

	db.imm = append(db.imm, immEntry{
		sl:      db.mem,
		walPath: oldWAL.Path(),
		seq:     db.memSeq,
	})

	// Open the new WAL BEFORE closing the old one so there is never a gap in
	// coverage (if this panics we still have oldWAL for recovery).
	seq := db.nextSeq.Add(1)
	db.memSeq = seq
	w, err := wal.Open(db.walPath(seq), !db.opts.NoSync)
	if err != nil {
		// Unrecoverable startup failure; surface as panic so the caller
		// knows the DB is broken.
		panic(fmt.Sprintf("gostore: open WAL %s: %v", db.walPath(seq), err))
	}
	db.memWAL = w
	db.mem = skiplist.New()

	// Close the old WAL now that all future writes go to the new one.
	_ = oldWAL.Close()

	select {
	case db.bgCh <- struct{}{}:
	default:
	}
}

// Get returns the value for key, or (nil, false, nil) if absent or deleted.
func (db *DB) Get(key []byte) ([]byte, bool, error) {
	if db.closed.Load() {
		return nil, false, ErrClosed
	}

	// ── In-memory search (under read lock) ──────────────────────────────────
	db.mu.RLock()

	if e, ok := db.mem.Get(key); ok {
		val := cloneBytes(e.Value)
		kind := e.Kind
		db.mu.RUnlock()
		if kind == record.KindTombstone {
			return nil, false, nil
		}
		return val, true, nil
	}
	for i := len(db.imm) - 1; i >= 0; i-- {
		if e, ok := db.imm[i].sl.Get(key); ok {
			val := cloneBytes(e.Value)
			kind := e.Kind
			db.mu.RUnlock()
			if kind == record.KindTombstone {
				return nil, false, nil
			}
			return val, true, nil
		}
	}

	// Retain SSTable refs before releasing the lock so compaction cannot
	// delete the files while we are reading them.
	tables := make([]*tableHandle, len(db.tables))
	copy(tables, db.tables)
	for _, th := range tables {
		th.retain()
	}
	db.mu.RUnlock()

	defer func() {
		for _, th := range tables {
			th.release()
		}
	}()

	// ── SSTable search (no lock; ReadAt is concurrent-safe) ─────────────────
	for _, th := range tables {
		v, kind, found, err := th.r.Get(key)
		if err != nil {
			return nil, false, fmt.Errorf("gostore get: %w", err)
		}
		if found {
			if kind == record.KindTombstone {
				return nil, false, nil
			}
			return v, true, nil
		}
	}
	return nil, false, nil
}

// Close flushes all in-memory data, waits for the background goroutine, and
// releases all resources.
func (db *DB) Close() error {
	if !db.closed.CompareAndSwap(false, true) {
		return ErrClosed
	}
	close(db.closeCh)
	db.closeWg.Wait()

	// Flush any remaining immutable memtables the background worker didn't finish.
	for {
		db.mu.RLock()
		pending := len(db.imm)
		db.mu.RUnlock()
		if pending == 0 {
			break
		}
		if err := db.flushImm(); err != nil {
			return fmt.Errorf("gostore close: flush imm: %w", err)
		}
	}

	// Close and optionally flush the active memtable.
	_ = db.memWAL.Close()
	if db.mem.Len() > 0 {
		th, err := db.flushOne(immEntry{
			sl:      db.mem,
			walPath: db.memWAL.Path(),
			seq:     db.memSeq,
		})
		if err != nil {
			return fmt.Errorf("gostore close: flush active: %w", err)
		}
		// No lock needed — background goroutine has exited, no concurrent writers.
		db.tables = append([]*tableHandle{th}, db.tables...)
	} else {
		_ = os.Remove(db.memWAL.Path())
	}

	// Close reader file handles. Do NOT call th.release() here — that
	// would decrement refs to 0 and delete the .sst files on disk, which
	// must survive for the next Open. Deletion only happens during
	// compaction when a superseded file is explicitly replaced.
	for _, th := range db.tables {
		_ = th.r.Close()
	}
	return nil
}

// ── Recovery ─────────────────────────────────────────────────────────────────

func (db *DB) recover() error {
	sstSeqs, walSeqs, maxSeq, err := scanDir(db.dir)
	if err != nil {
		return err
	}
	db.nextSeq.Store(maxSeq)

	// Open all existing SSTables, newest first (highest seq → index 0).
	for i := len(sstSeqs) - 1; i >= 0; i-- {
		sp := sstSeqs[i]
		r, err := sstable.Open(sp.path)
		if err != nil {
			return fmt.Errorf("recover: open sst %s: %w", sp.path, err)
		}
		db.tables = append(db.tables, newTableHandle(r, sp.path))
	}

	flushed := make(map[uint64]bool, len(sstSeqs))
	for _, sp := range sstSeqs {
		flushed[sp.seq] = true
	}

	// Replay un-flushed WALs in ascending order (oldest first).
	sort.Slice(walSeqs, func(i, j int) bool { return walSeqs[i].seq < walSeqs[j].seq })
	var activeWALSeq uint64
	for _, wp := range walSeqs {
		if flushed[wp.seq] {
			_ = os.Remove(wp.path) // already captured in SSTable
			continue
		}
		if err := wal.Replay(wp.path, func(r wal.Record) error {
			kind := record.KindValue
			if r.Type == wal.RecordDel {
				kind = record.KindTombstone
			}
			db.mem.Put(r.Key, r.Value, kind)
			return nil
		}); err != nil {
			return fmt.Errorf("recover: replay %s: %w", wp.path, err)
		}
		activeWALSeq = wp.seq // keep updating; end result = highest un-flushed seq
	}

	// Delete all un-flushed WALs except the one we'll reopen as active.
	for _, wp := range walSeqs {
		if !flushed[wp.seq] && wp.seq != activeWALSeq {
			_ = os.Remove(wp.path)
		}
	}

	// Open (or create) the active WAL.
	if activeWALSeq > 0 {
		db.memSeq = activeWALSeq
		w, err := wal.Open(db.walPath(activeWALSeq), !db.opts.NoSync)
		if err != nil {
			return fmt.Errorf("recover: reopen wal: %w", err)
		}
		db.memWAL = w
	} else {
		seq := db.nextSeq.Add(1)
		db.memSeq = seq
		w, err := wal.Open(db.walPath(seq), !db.opts.NoSync)
		if err != nil {
			return fmt.Errorf("recover: create wal: %w", err)
		}
		db.memWAL = w
	}
	return nil
}

type seqPath struct {
	seq  uint64
	path string
}

func scanDir(dir string) (ssts, wals []seqPath, maxSeq uint64, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, 0, err
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".sst"):
			seq, serr := seqFromName(name, ".sst")
			if serr != nil {
				continue
			}
			ssts = append(ssts, seqPath{seq, filepath.Join(dir, name)})
			if seq > maxSeq {
				maxSeq = seq
			}
		case strings.HasSuffix(name, ".wal"):
			seq, serr := seqFromName(name, ".wal")
			if serr != nil {
				continue
			}
			wals = append(wals, seqPath{seq, filepath.Join(dir, name)})
			if seq > maxSeq {
				maxSeq = seq
			}
		}
	}
	sort.Slice(ssts, func(i, j int) bool { return ssts[i].seq < ssts[j].seq })
	return ssts, wals, maxSeq, nil
}

func seqFromName(name, ext string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSuffix(name, ext), 10, 64)
}

// ── Flush ─────────────────────────────────────────────────────────────────────

// flushImm flushes the oldest immutable memtable to an SSTable.
// May be called without any lock held.
func (db *DB) flushImm() error {
	db.mu.RLock()
	if len(db.imm) == 0 {
		db.mu.RUnlock()
		return nil
	}
	entry := db.imm[0]
	db.mu.RUnlock()

	th, err := db.flushOne(entry)
	if err != nil {
		return err
	}

	db.mu.Lock()
	db.tables = append([]*tableHandle{th}, db.tables...)
	db.imm = db.imm[1:]
	db.mu.Unlock()

	_ = os.Remove(entry.walPath)
	return nil
}

// flushOne writes entry.sl to a new SSTable and returns its tableHandle.
// Does NOT acquire any lock; the WAL file at entry.walPath must already be
// closed before this is called.
func (db *DB) flushOne(entry immEntry) (*tableHandle, error) {
	sstPath := db.sstPath(entry.seq)
	w, err := sstable.NewWriter(sstPath, db.opts.BloomBitsPerKey)
	if err != nil {
		return nil, fmt.Errorf("flush: create sst: %w", err)
	}
	it := entry.sl.NewIterator()
	for {
		e, ok := it.Next()
		if !ok {
			break
		}
		if err := w.Add(e.Key, e.Value, e.Kind); err != nil {
			_ = w.Close()
			_ = os.Remove(sstPath)
			return nil, fmt.Errorf("flush: write entry: %w", err)
		}
	}
	if err := w.Finish(); err != nil {
		_ = os.Remove(sstPath)
		return nil, fmt.Errorf("flush: finish: %w", err)
	}
	r, err := sstable.Open(sstPath)
	if err != nil {
		_ = os.Remove(sstPath)
		return nil, fmt.Errorf("flush: open: %w", err)
	}
	return newTableHandle(r, sstPath), nil
}

// ── Background worker ─────────────────────────────────────────────────────────

func (db *DB) backgroundWorker() {
	defer db.closeWg.Done()
	for {
		select {
		case <-db.closeCh:
			return
		case <-db.bgCh:
			// Drain all pending immutable memtables before checking compaction.
			for {
				db.mu.RLock()
				n := len(db.imm)
				db.mu.RUnlock()
				if n == 0 {
					break
				}
				if err := db.flushImm(); err != nil {
					break
				}
			}
			if db.needsCompaction() {
				_ = db.compact()
			}
		}
	}
}

// ── Path helpers ──────────────────────────────────────────────────────────────

func (db *DB) walPath(seq uint64) string {
	return filepath.Join(db.dir, fmt.Sprintf("%020d.wal", seq))
}

func (db *DB) sstPath(seq uint64) string {
	return filepath.Join(db.dir, fmt.Sprintf("%020d.sst", seq))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
