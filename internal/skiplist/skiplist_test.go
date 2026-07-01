package skiplist

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/user/gostore/internal/record"
)

func TestPutAndGet(t *testing.T) {
	sl := New()
	cases := []struct{ key, val string }{
		{"apple", "red"},
		{"banana", "yellow"},
		{"cherry", "crimson"},
	}
	for _, c := range cases {
		sl.Put([]byte(c.key), []byte(c.val), record.KindValue)
	}
	for _, c := range cases {
		e, ok := sl.Get([]byte(c.key))
		require.True(t, ok)
		require.Equal(t, c.val, string(e.Value))
		require.Equal(t, record.KindValue, e.Kind)
	}
}

func TestOverwrite(t *testing.T) {
	sl := New()
	sl.Put([]byte("k"), []byte("v1"), record.KindValue)
	sl.Put([]byte("k"), []byte("v2"), record.KindValue)
	e, ok := sl.Get([]byte("k"))
	require.True(t, ok)
	require.Equal(t, "v2", string(e.Value))
	require.Equal(t, 1, sl.Len(), "overwrite must not increase length")
}

func TestTombstone(t *testing.T) {
	sl := New()
	sl.Put([]byte("k"), []byte("v"), record.KindValue)
	sl.Put([]byte("k"), nil, record.KindTombstone)
	e, ok := sl.Get([]byte("k"))
	require.True(t, ok, "tombstone must be visible")
	require.Equal(t, record.KindTombstone, e.Kind)
}

func TestMissing(t *testing.T) {
	sl := New()
	sl.Put([]byte("a"), []byte("1"), record.KindValue)
	_, ok := sl.Get([]byte("missing"))
	require.False(t, ok)
}

func TestIteratorOrder(t *testing.T) {
	sl := New()
	keys := []string{"dog", "ant", "cat", "bee"}
	for _, k := range keys {
		sl.Put([]byte(k), []byte(k), record.KindValue)
	}
	it := sl.NewIterator()
	var got []string
	for {
		e, ok := it.Next()
		if !ok {
			break
		}
		got = append(got, string(e.Key))
	}
	require.Equal(t, []string{"ant", "bee", "cat", "dog"}, got)
}

func TestIteratorEmpty(t *testing.T) {
	sl := New()
	it := sl.NewIterator()
	_, ok := it.Next()
	require.False(t, ok)
}

func TestSizeTracking(t *testing.T) {
	sl := New()
	before := sl.Size()
	sl.Put([]byte("key"), []byte("value"), record.KindValue)
	require.Greater(t, sl.Size(), before)
}

func TestManyKeys(t *testing.T) {
	sl := New()
	const n = 1000
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		sl.Put(k, k, record.KindValue)
	}
	require.Equal(t, n, sl.Len())

	// Verify all keys are retrievable.
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		e, ok := sl.Get(k)
		require.True(t, ok)
		require.Equal(t, k, e.Value)
	}

	// Verify iterator order.
	it := sl.NewIterator()
	prev := []byte{}
	count := 0
	for {
		e, ok := it.Next()
		if !ok {
			break
		}
		require.True(t, len(prev) == 0 || string(e.Key) > string(prev))
		prev = e.Key
		count++
	}
	require.Equal(t, n, count)
}

func BenchmarkPut(b *testing.B) {
	sl := New()
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		sl.Put(k, k, record.KindValue)
	}
}

func BenchmarkGet(b *testing.B) {
	sl := New()
	const n = 10000
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("key%08d", i))
		sl.Put(k, k, record.KindValue)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("key%08d", i%n))
		sl.Get(k)
	}
}
