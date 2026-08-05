package main

import "testing"

func TestTripsCircuitBreaker(t *testing.T) {
	tests := []struct {
		name     string
		requests int
		errors   int
		want     bool
	}{
		{name: "below minimum samples, even at 100% errors", requests: 5, errors: 5, want: false},
		{name: "at minimum samples, error rate right at threshold", requests: 20, errors: 10, want: true},
		{name: "at minimum samples, just under threshold", requests: 20, errors: 9, want: false},
		{name: "well past minimum samples, healthy error rate", requests: 1000, errors: 5, want: false},
		{name: "well past minimum samples, unhealthy error rate", requests: 1000, errors: 600, want: true},
		{name: "no requests yet", requests: 0, errors: 0, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Metrics{Requests: tc.requests, Errors: tc.errors}
			if got := tripsCircuitBreaker(m); got != tc.want {
				t.Fatalf("tripsCircuitBreaker(requests=%d, errors=%d) = %v, want %v", tc.requests, tc.errors, got, tc.want)
			}
		})
	}
}

func TestErrorRate(t *testing.T) {
	tests := []struct {
		name     string
		requests int
		errors   int
		want     float64
	}{
		{name: "no requests", requests: 0, errors: 0, want: 0},
		{name: "no errors", requests: 100, errors: 0, want: 0},
		{name: "all errors", requests: 100, errors: 100, want: 1.0},
		{name: "half errors", requests: 100, errors: 50, want: 0.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Metrics{Requests: tc.requests, Errors: tc.errors}
			if got := errorRate(m); got != tc.want {
				t.Fatalf("errorRate(requests=%d, errors=%d) = %v, want %v", tc.requests, tc.errors, got, tc.want)
			}
		})
	}
}
