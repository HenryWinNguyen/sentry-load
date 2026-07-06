package main

import (
	"fmt"
	"strconv"
)

// Hard caps, per CLAUDE.md's "hard caps always on" constraint. A job
// exceeding these is rejected outright, not silently clamped — silently
// under-delivering is exactly the anti-pattern SCOPE.md argues against.
const (
	maxVUs             = 200
	maxDurationSeconds = 300
	maxSafeRPS         = 500
)

// Job is this worker's view of the job contract published by the
// coordinator. Deliberately not shared as a Go type across the two modules
// — they're independent deployables that agree on a JSON/stream-field wire
// format, not on shared code.
type Job struct {
	ID              string
	URL             string
	VUs             int
	DurationSeconds int
	RampPattern     string
}

func parseJob(values map[string]interface{}) (Job, error) {
	var job Job

	str := func(key string) (string, error) {
		v, ok := values[key]
		if !ok {
			return "", fmt.Errorf("missing field %q", key)
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("field %q is not a string: %v", key, v)
		}
		return s, nil
	}

	var err error
	if job.ID, err = str("id"); err != nil {
		return Job{}, err
	}
	if job.URL, err = str("url"); err != nil {
		return Job{}, err
	}
	if job.RampPattern, err = str("ramp_pattern"); err != nil {
		return Job{}, err
	}

	vusStr, err := str("vus")
	if err != nil {
		return Job{}, err
	}
	if job.VUs, err = strconv.Atoi(vusStr); err != nil {
		return Job{}, fmt.Errorf("invalid vus %q: %w", vusStr, err)
	}

	durStr, err := str("duration_seconds")
	if err != nil {
		return Job{}, err
	}
	if job.DurationSeconds, err = strconv.Atoi(durStr); err != nil {
		return Job{}, fmt.Errorf("invalid duration_seconds %q: %w", durStr, err)
	}

	switch job.RampPattern {
	case "steady", "ramp":
	default:
		return Job{}, fmt.Errorf("unknown ramp_pattern %q (want steady or ramp)", job.RampPattern)
	}

	return job, nil
}

// validate enforces the hard caps. Returns a non-nil error describing why
// the job is rejected; callers must not run a job that fails validation.
func (j Job) validate() error {
	if j.VUs <= 0 || j.VUs > maxVUs {
		return fmt.Errorf("vus %d out of allowed range (1-%d)", j.VUs, maxVUs)
	}
	if j.DurationSeconds <= 0 || j.DurationSeconds > maxDurationSeconds {
		return fmt.Errorf("duration_seconds %d out of allowed range (1-%d)", j.DurationSeconds, maxDurationSeconds)
	}
	return nil
}
