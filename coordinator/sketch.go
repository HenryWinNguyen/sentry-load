package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Decoding and merging of the workers' latency sketches (worker/sketch.go
// builds and documents them). Not shared Go code with the worker, same as
// every other part of the wire contract — the two sides agree on the "v1:"
// format, including its 1% relative accuracy, not on a common package.
//
// This is what makes a real test-wide p95 possible: summing bucket counts
// across workers yields exactly the histogram of every request in the
// test, so a percentile read off the merged sketch is within 1% of the
// true combined percentile. Averaging or taking the max of per-worker p95s
// would not be — M6 showed workers on different infra have genuinely
// different latency distributions.
const (
	sketchRelativeAccuracy = 0.01
	sketchFormatPrefix     = "v1:"
)

var sketchGamma = (1 + sketchRelativeAccuracy) / (1 - sketchRelativeAccuracy)

// latencySketch is a decoded bucket-index -> count histogram.
type latencySketch struct {
	counts map[int]uint64
	total  uint64
}

func decodeLatencySketch(s string) (*latencySketch, error) {
	body, ok := strings.CutPrefix(s, sketchFormatPrefix)
	if !ok {
		return nil, fmt.Errorf("unsupported sketch format %.10q", s)
	}
	sk := &latencySketch{counts: make(map[int]uint64)}
	if body == "" {
		return sk, nil
	}
	for _, pair := range strings.Split(body, ",") {
		idxStr, countStr, ok := strings.Cut(pair, ":")
		if !ok {
			return nil, fmt.Errorf("malformed sketch bucket %q", pair)
		}
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return nil, fmt.Errorf("malformed sketch bucket index %q: %w", idxStr, err)
		}
		count, err := strconv.ParseUint(countStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed sketch bucket count %q: %w", countStr, err)
		}
		sk.counts[idx] += count
		sk.total += count
	}
	return sk, nil
}

// merge adds other's counts into s.
func (s *latencySketch) merge(other *latencySketch) {
	for idx, c := range other.counts {
		s.counts[idx] += c
	}
	s.total += other.total
}

// quantileMS returns the q-quantile (0..1) in milliseconds, using the same
// rank convention as the worker's exact per-worker percentiles (the
// sample at index floor(q*(n-1)) in sorted order). ok=false for an empty
// sketch.
func (s *latencySketch) quantileMS(q float64) (float64, bool) {
	if s.total == 0 {
		return 0, false
	}
	rank := uint64(q * float64(s.total-1))

	idxs := make([]int, 0, len(s.counts))
	for idx := range s.counts {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)

	var seen uint64
	for _, idx := range idxs {
		seen += s.counts[idx]
		if seen > rank {
			return bucketValueMicros(idx) / 1000, true
		}
	}
	return bucketValueMicros(idxs[len(idxs)-1]) / 1000, true
}

// bucketValueMicros is the representative value of bucket idx: the point
// within (gamma^(idx-1), gamma^idx] whose relative distance to both ends
// is equal, which is what bounds the error at sketchRelativeAccuracy.
func bucketValueMicros(idx int) float64 {
	if idx <= 0 {
		return 1
	}
	return 2 * math.Pow(sketchGamma, float64(idx)) / (sketchGamma + 1)
}
