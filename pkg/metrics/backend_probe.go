package mpcmetrics

import (
	"context"
	"errors"
	"sync/atomic"
)

var errorThreshold = 0.5

type BackendProbe struct {
	successes atomic.Uint32
	failures  atomic.Uint32
}

func NewBackendProbe() AvailabilityProbe {
	return &BackendProbe{
		successes: atomic.Uint32{},
		failures:  atomic.Uint32{},
	}
}

func (q *BackendProbe) CheckAvailability(_ context.Context) error {
	// if we split the loads and the stores into two separate operations, we
	// could miss some success and failure events, so instead load and store
	// atomically in one operation
	successes := float64(q.successes.Swap(0))
	failures := float64(q.failures.Swap(0))

	switch {
	// if success+failures == 0: nothing happened; we need to handle it
	//    separately to avoid division by 0.
	// if success+failures == 1:
	//   failures == 1: we don't have enough data to fire an alarm
	//   successes == 1: everything is good
	case successes+failures <= 1:
		return nil
	case failures/(successes+failures) > errorThreshold:
		return errors.New("failure threshold high")
	default:
		return nil
	}
}

func (q *BackendProbe) Success() {
	q.successes.Add(1)
}

func (q *BackendProbe) Failure() {
	q.failures.Add(1)
}
