// Package record defines shared types used across memtable and SSTable layers.
package record

// Kind distinguishes a live value from a deletion tombstone.
type Kind uint8

const (
	// KindValue marks a live key-value entry.
	KindValue Kind = 0
	// KindTombstone marks a deleted key (written by Delete).
	KindTombstone Kind = 1
)
