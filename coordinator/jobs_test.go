package main

import "testing"

func TestSplitVUs(t *testing.T) {
	tests := []struct {
		name            string
		totalVUs        int
		fanout          int
		want            []int
	}{
		{name: "evenly divisible", totalVUs: 20, fanout: 2, want: []int{10, 10}},
		{name: "remainder goes to the first sub-jobs", totalVUs: 20, fanout: 3, want: []int{7, 7, 6}},
		{name: "single worker gets everything", totalVUs: 20, fanout: 1, want: []int{20}},
		{name: "fewer VUs than workers still sums correctly", totalVUs: 1, fanout: 3, want: []int{1, 0, 0}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitVUs(tc.totalVUs, tc.fanout)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			sum := 0
			for i, v := range got {
				if v != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
				sum += v
			}
			if sum != tc.totalVUs {
				t.Fatalf("sub-job VUs sum to %d, want %d", sum, tc.totalVUs)
			}
		})
	}
}

func TestBuildSubJobsUniqueJobIDs(t *testing.T) {
	specs := buildSubJobs(20, 3)
	if len(specs) != 3 {
		t.Fatalf("got %d sub-jobs, want 3", len(specs))
	}

	seen := make(map[string]bool)
	total := 0
	for _, spec := range specs {
		if spec.JobID == "" {
			t.Fatal("expected a non-empty job ID")
		}
		if seen[spec.JobID] {
			t.Fatalf("duplicate job ID %q", spec.JobID)
		}
		seen[spec.JobID] = true
		total += spec.VUs
	}
	if total != 20 {
		t.Fatalf("VUs sum to %d, want 20", total)
	}
}
