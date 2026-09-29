package main

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mergeable latency sketch, so the coordinator can report one true
// test-wide p50/p95/p99 across every worker instead of only per-worker
// numbers. Percentiles themselves can't be merged (the p95 of two p95s
// isn't the combined p95), but histograms can: summing bucket counts
// gives exactly the histogram of the combined samples.
//
// The buckets are logarithmic, the same construction as DDSketch: bucket i
// covers (gamma^(i-1), gamma^i] microseconds with gamma = (1+a)/(1-a), so
// any value reported from a bucket is within relative error a of the true
// sample — 1% here, whether the latency is 2ms or 2s. A test's whole
// latency range fits in a few hundred buckets, so the sketch rides along on
// every per-second metrics update without meaningfully growing it.
//
// Wire format (the coordinator decodes it independently, see
// coordinator/sketch.go): "v1:" + comma-separated "bucket:count" pairs in
// ascending bucket order. Bucket 0 holds sub-microsecond samples. The
// version prefix pins the accuracy parameter, so a mismatch between worker
// and coordinator versions fails loudly instead of decoding wrong numbers.
const (
	sketchRelativeAccuracy = 0.01
	sketchFormatPrefix     = "v1:"
)

var sketchLogGamma = math.Log((1 + sketchRelativeAccuracy) / (1 - sketchRelativeAccuracy))

// sketchBucket maps a latency to its bucket index.
func sketchBucket(d time.Duration) int {
	us := float64(d) / float64(time.Microsecond)
	if us <= 1 {
		return 0
	}
	return int(math.Ceil(math.Log(us) / sketchLogGamma))
}

// encodeLatencySketch builds and serializes the sketch of latencies.
func encodeLatencySketch(latencies []time.Duration) string {
	counts := make(map[int]int)
	for _, d := range latencies {
		counts[sketchBucket(d)]++
	}
	buckets := make([]int, 0, len(counts))
	for b := range counts {
		buckets = append(buckets, b)
	}
	sort.Ints(buckets)

	var sb strings.Builder
	sb.WriteString(sketchFormatPrefix)
	for i, b := range buckets {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(b))
		sb.WriteByte(':')
		sb.WriteString(strconv.Itoa(counts[b]))
	}
	return sb.String()
}
