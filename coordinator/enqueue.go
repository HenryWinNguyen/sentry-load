package main

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const jobsStream = "sentry:jobs"

// redisEnqueuer is the production jobEnqueuer: splits a test into sub-jobs
// and XADDs each onto sentry:jobs for the worker consumer group to pick up.
type redisEnqueuer struct {
	rdb *redis.Client
}

func (e *redisEnqueuer) Enqueue(ctx context.Context, testURL, rampPattern string, totalVUs, durationSeconds, fanout int) (string, []string, error) {
	testID := uuid.NewString()
	specs := buildSubJobs(totalVUs, fanout)
	subJobIDs := make([]string, len(specs))

	for i, spec := range specs {
		subJobIDs[i] = spec.JobID
		job := map[string]interface{}{
			"id":               spec.JobID,
			"test_id":          testID,
			"url":              testURL,
			"vus":              strconv.Itoa(spec.VUs),
			"duration_seconds": strconv.Itoa(durationSeconds),
			"ramp_pattern":     rampPattern,
		}
		if _, err := e.rdb.XAdd(ctx, &redis.XAddArgs{Stream: jobsStream, Values: job}).Result(); err != nil {
			return "", nil, err
		}
	}

	return testID, subJobIDs, nil
}
