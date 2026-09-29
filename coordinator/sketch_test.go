package main

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// encodeForTest mirrors worker/sketch.go's encoder. The two modules share
// a wire format, not code, so this is the coordinator's executable
// statement of what it expects that format to be.
func encodeForTest(latencies []time.Duration) string {
	logGamma := math.Log(sketchGamma)
	counts := map[int]int{}
	for _, d := range latencies {
		us := float64(d) / float64(time.Microsecond)
		idx := 0
		if us > 1 {
			idx = int(math.Ceil(math.Log(us) / logGamma))
		}
		counts[idx]++
	}
	idxs := make([]int, 0, len(counts))
	for i := range counts {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	parts := make([]string, len(idxs))
	for i, idx := range idxs {
		parts[i] = strconv.Itoa(idx) + ":" + strconv.Itoa(counts[idx])
	}
	return sketchFormatPrefix + strings.Join(parts, ",")
}

func exactQuantileMS(latencies []time.Duration, q float64) float64 {
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return float64(sorted[int(q*float64(len(sorted)-1))]) / float64(time.Millisecond)
}

func randomLatencies(r *rand.Rand, n int, base, spread time.Duration) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = base + time.Duration(r.ExpFloat64()*float64(spread))
	}
	return out
}

// The case that motivated sketches: a fast worker next to the target and a
// slow cross-region one. The merged p95 must match the p95 of all requests
// together — which neither worker's own p95, nor their average, does.
func TestMergedSketchMatchesExactCombinedPercentiles(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	fast := randomLatencies(r, 20000, 2*time.Millisecond, 3*time.Millisecond)
	slow := randomLatencies(r, 5000, 80*time.Millisecond, 60*time.Millisecond)
	all := append(append([]time.Duration(nil), fast...), slow...)

	merged, err := decodeLatencySketch(encodeForTest(fast))
	if err != nil {
		t.Fatal(err)
	}
	other, err := decodeLatencySketch(encodeForTest(slow))
	if err != nil {
		t.Fatal(err)
	}
	merged.merge(other)

	for _, q := range []float64{0.5, 0.95, 0.99} {
		got, ok := merged.quantileMS(q)
		if !ok {
			t.Fatal("empty merged sketch")
		}
		want := exactQuantileMS(all, q)
		if rel := math.Abs(got-want) / want; rel > sketchRelativeAccuracy {
			t.Errorf("p%.0f: got %.3fms, exact %.3fms (%.2f%% off, bound %.0f%%)", q*100, got, want, rel*100, sketchRelativeAccuracy*100)
		}
	}

	naive := (exactQuantileMS(fast, 0.95) + exactQuantileMS(slow, 0.95)) / 2
	exact := exactQuantileMS(all, 0.95)
	if math.Abs(naive-exact)/exact < 0.1 {
		t.Fatalf("test data doesn't exercise the problem: averaged p95 %.1f is close to exact %.1f", naive, exact)
	}
}

func TestDecodeLatencySketchRejectsBadInput(t *testing.T) {
	for _, in := range []string{"", "v2:1:1", "v1:abc", "v1:1:x", "v1:1"} {
		if _, err := decodeLatencySketch(in); err == nil {
			t.Errorf("decodeLatencySketch(%q) accepted malformed input", in)
		}
	}
	sk, err := decodeLatencySketch("v1:")
	if err != nil || sk.total != 0 {
		t.Fatalf("empty v1 sketch: %v %+v", err, sk)
	}
	if _, ok := sk.quantileMS(0.5); ok {
		t.Fatal("empty sketch reported a quantile")
	}
}

func TestSnapshotCombinedPercentilesAcrossWorkers(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	fast := randomLatencies(r, 3000, time.Millisecond, 2*time.Millisecond)
	slow := randomLatencies(r, 1000, 100*time.Millisecond, 50*time.Millisecond)

	s := NewTestStore()
	s.Register("t", "u", "https://example.com", []string{"a", "b"})
	s.Update("t", "a", resultUpdate{Requests: len(fast), Done: true, LatencySketch: encodeForTest(fast)})
	s.Update("t", "b", resultUpdate{Requests: len(slow), Done: true, LatencySketch: encodeForTest(slow)})

	snap, _ := s.Snapshot("t", "u")
	if snap.P95MS == nil {
		t.Fatal("no combined p95 despite every worker sending a sketch")
	}
	want := exactQuantileMS(append(fast, slow...), 0.95)
	if rel := math.Abs(*snap.P95MS-want) / want; rel > sketchRelativeAccuracy+0.001 { // +rounding to 0.1ms
		t.Fatalf("combined p95 %.1fms vs exact %.1fms", *snap.P95MS, want)
	}
}

func TestSnapshotOmitsCombinedPercentilesWhenAWorkerSentNoSketch(t *testing.T) {
	s := NewTestStore()
	s.Register("t", "u", "https://example.com", []string{"a", "b"})
	s.Update("t", "a", resultUpdate{Requests: 10, LatencySketch: encodeForTest([]time.Duration{time.Millisecond})})
	s.Update("t", "b", resultUpdate{Requests: 10}) // older worker binary

	snap, _ := s.Snapshot("t", "u")
	if snap.P50MS != nil || snap.P95MS != nil || snap.P99MS != nil {
		t.Fatal("reported combined percentiles computed from only some workers' samples")
	}
}

func TestSnapshotIgnoresIdleSubJobsWhenMerging(t *testing.T) {
	s := NewTestStore()
	s.Register("t", "u", "https://example.com", []string{"a", "b"})
	s.Update("t", "a", resultUpdate{Requests: 1, LatencySketch: encodeForTest([]time.Duration{5 * time.Millisecond})})
	// b hasn't reported anything yet — not a reason to withhold a's numbers.

	snap, _ := s.Snapshot("t", "u")
	if snap.P50MS == nil {
		t.Fatal("a sub-job with no requests yet blocked the combined percentiles")
	}
}
