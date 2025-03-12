package mpcmetrics

import (
	"context"
	"fmt"
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
	successes := q.successes.Swap(0)
	failures := q.failures.Swap(0)
	if successes == 0 {
		//ok, let's consider > 1 error and 0 success not a good sign...
		if failures > 1 {
			return fmt.Errorf("failure threshold high")
		}
	} else {
		//non-zero successes here, let's check the error to success rate
		if float64(failures)/float64(successes) > errorThreshold {
			return fmt.Errorf("failure threshold high")
		}
	}
	return nil
}

func (q *BackendProbe) Success() {
	q.successes.Add(1)
}

func (q *BackendProbe) Failure() {
	q.failures.Add(1)
}
