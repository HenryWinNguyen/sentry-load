package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestEncodeLatencySketchFormat(t *testing.T) {
	got := encodeLatencySketch([]time.Duration{0, 5 * time.Millisecond, 5 * time.Millisecond, time.Second})
	if !strings.HasPrefix(got, sketchFormatPrefix) {
		t.Fatalf("missing version prefix: %q", got)
	}
	pairs := strings.Split(strings.TrimPrefix(got, sketchFormatPrefix), ",")
	if len(pairs) != 3 || pairs[0] != "0:1" || !strings.HasSuffix(pairs[1], ":2") {
		t.Fatalf("unexpected encoding %q", got)
	}
	if encodeLatencySketch(nil) != sketchFormatPrefix {
		t.Fatal("empty input should encode to just the prefix")
	}
}

// Every value must land in a bucket whose representative value (the one
// the coordinator reports) is within the promised relative accuracy.
func TestSketchBucketRelativeError(t *testing.T) {
	gamma := math.Exp(sketchLogGamma)
	for us := 2.0; us < 30e6; us *= 1.37 {
		d := time.Duration(us * float64(time.Microsecond))
		idx := sketchBucket(d)
		rep := 2 * math.Pow(gamma, float64(idx)) / (gamma + 1)
		actual := float64(d) / float64(time.Microsecond)
		if rel := math.Abs(rep-actual) / actual; rel > sketchRelativeAccuracy+1e-9 {
			t.Fatalf("%v -> bucket %d rep %.2fus, %.3f%% off", d, idx, rep, rel*100)
		}
	}
}
