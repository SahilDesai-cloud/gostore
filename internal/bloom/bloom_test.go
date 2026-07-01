package bloom

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBasicAddAndContains(t *testing.T) {
	f := NewWithEstimate(1000, 0.01)
	keys := []string{"apple", "banana", "cherry", "date", "elderberry"}
	for _, k := range keys {
		f.Add([]byte(k))
	}
	for _, k := range keys {
		require.True(t, f.MayContain([]byte(k)), "present key must be found: %s", k)
	}
}

func TestDefiniteNegative(t *testing.T) {
	f := NewWithEstimate(100, 0.001)
	f.Add([]byte("present"))
	require.False(t, f.MayContain([]byte("absent_XYZ_abc")))
}

func TestFalsePositiveRate(t *testing.T) {
	const n = 10_000
	const targetFP = 0.01
	f := NewWithEstimate(n, targetFP)

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key-%d", rng.Int63())
		f.Add([]byte(k))
	}

	// Test with keys guaranteed NOT to be in the filter.
	fps := 0
	const probes = 100_000
	for i := 0; i < probes; i++ {
		k := fmt.Sprintf("absent-%d-%d", rng.Int63(), rng.Int63())
		if f.MayContain([]byte(k)) {
			fps++
		}
	}
	fpRate := float64(fps) / probes
	// Allow 3× the target rate to account for statistical variance.
	require.Less(t, fpRate, targetFP*3, "FP rate %.4f exceeds 3× target", fpRate)
}

func TestRoundTrip(t *testing.T) {
	f := NewWithEstimate(500, 0.01)
	for i := 0; i < 500; i++ {
		f.Add([]byte(fmt.Sprintf("k%d", i)))
	}
	data := f.Bytes()
	f2, err := FromBytes(data)
	require.NoError(t, err)

	for i := 0; i < 500; i++ {
		k := []byte(fmt.Sprintf("k%d", i))
		require.True(t, f2.MayContain(k))
	}
}

func TestFromBytesErrors(t *testing.T) {
	_, err := FromBytes([]byte{1, 2, 3})
	require.Error(t, err)
}

func BenchmarkAdd(b *testing.B) {
	f := NewWithEstimate(b.N+1, 0.01)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := []byte(fmt.Sprintf("k%d", i))
		f.Add(k)
	}
}

func BenchmarkMayContain(b *testing.B) {
	f := NewWithEstimate(10000, 0.01)
	for i := 0; i < 10000; i++ {
		f.Add([]byte(fmt.Sprintf("k%d", i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.MayContain([]byte(fmt.Sprintf("k%d", i%10000)))
	}
}
