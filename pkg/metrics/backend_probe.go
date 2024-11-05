package mpcmetrics

import (
	"context"
	"fmt"
	"sync/atomic"
)

var errorThreshold = 0.5

type BackendProbe struct {
	successes atomic.Int32
	failures  atomic.Int32
}

func NewBackendProbe() AvailabilityProbe {
	return &BackendProbe{
		successes: atomic.Int32{},
		failures:  atomic.Int32{},
	}
}

func (q *BackendProbe) CheckAvailability(_ context.Context) error {
	defer q.successes.Store(0)
	defer q.failures.Store(0)
	if q.successes.Load() == 0 {
		//ok, let's consider > 1 error and 0 success not a good sign...
		if q.failures.Load() > 1 {
			return fmt.Errorf("failure threshold high")
		}
	} else {
		//non-zero successes here, let's check the error to success rate
		if float64(q.failures.Load())/float64(q.successes.Load()) > errorThreshold {
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
