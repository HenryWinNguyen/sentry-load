package main

import (
	"errors"
	"testing"
)

func TestIsNoGroup(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "real NOGROUP error", err: errors.New("NOGROUP No such key 'sentry:jobs' or consumer group 'workers' in XREADGROUP with GROUP option"), want: true},
		{name: "unrelated redis error", err: errors.New("READONLY You can't write against a read only replica"), want: false},
		{name: "short error, shorter than the prefix", err: errors.New("nope"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNoGroup(tc.err); got != tc.want {
				t.Fatalf("isNoGroup(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsBusyGroup(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "real BUSYGROUP error", err: errors.New("BUSYGROUP Consumer Group name already exists"), want: true},
		{name: "unrelated redis error", err: errors.New("NOGROUP No such key"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBusyGroup(tc.err); got != tc.want {
				t.Fatalf("isBusyGroup(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
