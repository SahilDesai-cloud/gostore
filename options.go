// Package gostore is an embeddable, persistent key-value store built on an
// LSM (log-structured merge) tree. It supports concurrent readers with a
// single writer and survives abrupt process crashes without data loss for any
// acknowledged write.
package gostore

// CompactionStrategy selects the background compaction algorithm.
type CompactionStrategy int

const (
	// SizeTiered merges SSTables of similar size into one larger file.
	// Favours write throughput; allows higher read amplification than Leveled.
	SizeTiered CompactionStrategy = iota
	// Leveled maintains non-overlapping key ranges within each level and
	// enforces a per-level size ratio, bounding read amplification.
	Leveled
)

// Options configures a DB instance. Zero values are safe defaults.
type Options struct {
	// MemTableSize is the byte threshold at which the active memtable is
	// frozen and scheduled for flushing to an SSTable.
	// Default: 4 MiB.
	MemTableSize int64

	// BloomBitsPerKey controls the bloom-filter density per SSTable.
	// 10 gives ~1 % false-positive rate. Set to 0 to disable bloom filters.
	// Default: 10.
	BloomBitsPerKey int

	// Compaction selects the background compaction strategy.
	// Default: SizeTiered.
	Compaction CompactionStrategy

	// NoSync disables fsync after each WAL write. Faster but loses data on a
	// power failure (OS crash is still safe). Default: false (fsync enabled).
	NoSync bool

	// L0CompactionThreshold is the number of L0 SSTable files that triggers
	// a compaction. Applies to both SizeTiered and Leveled strategies.
	// Default: 4.
	L0CompactionThreshold int

	// LevelSizeMultiplier is the size ratio between adjacent levels for
	// Leveled compaction (L_{i+1} max size = L_i max size × multiplier).
	// Default: 10.
	LevelSizeMultiplier int
}

func (o *Options) setDefaults() {
	if o.MemTableSize <= 0 {
		o.MemTableSize = 4 << 20 // 4 MiB
	}
	if o.BloomBitsPerKey == 0 {
		o.BloomBitsPerKey = 10
	}
	if o.L0CompactionThreshold <= 0 {
		o.L0CompactionThreshold = 4
	}
	if o.LevelSizeMultiplier <= 0 {
		o.LevelSizeMultiplier = 10
	}
}
