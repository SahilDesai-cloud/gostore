package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	gostore "github.com/SahilDesai-cloud/gostore"
)

func main() {
	dir := filepath.Join(os.TempDir(), "gostore-demo")
	_ = os.RemoveAll(dir)

	fmt.Println("=== gostore demo ===")
	fmt.Println()

	// ── Open ─────────────────────────────────────────────────────────────────
	db, err := gostore.Open(dir, gostore.Options{
		MemTableSize:    1 << 20, // 1 MiB
		BloomBitsPerKey: 10,
	})
	must(err)
	fmt.Printf("Opened database at %s\n\n", dir)

	// ── Writes ───────────────────────────────────────────────────────────────
	entries := [][2]string{
		{"fruit:apple", "red and crunchy"},
		{"fruit:banana", "yellow and sweet"},
		{"fruit:cherry", "small and tart"},
		{"fruit:date", "brown and sticky"},
		{"fruit:elderberry", "dark purple"},
		{"veggie:carrot", "orange root"},
		{"veggie:daikon", "white radish"},
		{"veggie:edamame", "green soybean"},
	}
	for _, kv := range entries {
		must(db.Put([]byte(kv[0]), []byte(kv[1])))
	}
	fmt.Printf("Wrote %d keys\n\n", len(entries))

	// ── Point read ───────────────────────────────────────────────────────────
	v, found, err := db.Get([]byte("fruit:cherry"))
	must(err)
	fmt.Printf("Get(fruit:cherry)  →  %q  found=%v\n", v, found)

	_, found, err = db.Get([]byte("fruit:fig"))
	must(err)
	fmt.Printf("Get(fruit:fig)     →  (nil)  found=%v\n\n", found)

	// ── Delete ───────────────────────────────────────────────────────────────
	must(db.Delete([]byte("veggie:daikon")))
	_, found, err = db.Get([]byte("veggie:daikon"))
	must(err)
	fmt.Printf("After Delete(veggie:daikon): found=%v\n\n", found)

	// ── Range scan: all fruits ────────────────────────────────────────────────
	fmt.Println("Scan(fruit:, fruit;)  [all fruits, sorted]")
	it, err := db.Scan([]byte("fruit:"), []byte("fruit;"))
	must(err)
	for it.Next() {
		fmt.Printf("  %-20s = %s\n", it.Key(), it.Value())
	}
	must(it.Err())
	it.Close()
	fmt.Println()

	// ── Full scan ─────────────────────────────────────────────────────────────
	fmt.Println("Scan(nil, nil)  [all live keys]")
	it, err = db.Scan(nil, nil)
	must(err)
	count := 0
	for it.Next() {
		fmt.Printf("  %-20s = %s\n", it.Key(), it.Value())
		count++
	}
	must(it.Err())
	it.Close()
	fmt.Printf("  (%d keys total)\n\n", count)

	// ── Close + reopen (persistence check) ───────────────────────────────────
	must(db.Close())
	fmt.Println("Closed database.")

	db2, err := gostore.Open(dir, gostore.Options{BloomBitsPerKey: 10})
	must(err)
	defer db2.Close()
	fmt.Println("Reopened database (recovery from WAL/SSTables).")

	v, found, err = db2.Get([]byte("fruit:banana"))
	must(err)
	fmt.Printf("Get(fruit:banana) after restart  →  %q  found=%v\n", v, found)

	_, found, err = db2.Get([]byte("veggie:daikon"))
	must(err)
	fmt.Printf("Get(veggie:daikon) after restart  →  (nil)  found=%v  [delete survived]\n", found)

	fmt.Println()
	fmt.Println("Done.")
}

func must(err error) {
	if err != nil {
		log.Fatalf("error: %v", err)
	}
}
