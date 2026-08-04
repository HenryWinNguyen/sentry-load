package main

import "github.com/google/uuid"

// Hard caps enforced at submission time, before a job ever reaches Redis —
// mirrors the same caps the worker enforces independently (worker/job.go).
// Not shared as a Go type/const across modules on purpose (see CLAUDE.md);
// checking here just means a bad request fails fast with a clear API error
// instead of silently vanishing into a worker log.
const (
	maxVUsPerTest      = 200
	maxDurationSeconds = 300
	maxFanout          = 5
)

// subJobSpec is one worker's share of a test: a fresh job ID and its VU
// allocation.
type subJobSpec struct {
	JobID string
	VUs   int
}

// splitVUs divides totalVUs as evenly as possible across fanout sub-jobs;
// any remainder goes to the first few sub-jobs. Callers are responsible for
// keeping fanout <= totalVUs so no sub-job ends up with zero VUs.
func splitVUs(totalVUs, fanout int) []int {
	base, remainder := totalVUs/fanout, totalVUs%fanout
	vus := make([]int, fanout)
	for i := range vus {
		vus[i] = base
		if i < remainder {
			vus[i]++
		}
	}
	return vus
}

// buildSubJobs generates fanout sub-job specs (fresh job IDs + VU split)
// for one test.
func buildSubJobs(totalVUs, fanout int) []subJobSpec {
	counts := splitVUs(totalVUs, fanout)
	specs := make([]subJobSpec, fanout)
	for i, vus := range counts {
		specs[i] = subJobSpec{JobID: uuid.NewString(), VUs: vus}
	}
	return specs
}
