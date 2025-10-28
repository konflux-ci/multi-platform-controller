package taskrun

import (
	"context"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// isTransientAPIError checks if an error is a transient API server error that should be retried.
// This includes etcd timeout errors, temporary unavailability, and other transient failures.
func isTransientAPIError(err error) bool {
	if err == nil {
		return false
	}

	errorMsg := err.Error()
	transientErrors := []string{
		"etcdserver: request timed out",
	}

	for _, transientErr := range transientErrors {
		if strings.Contains(errorMsg, transientErr) {
			return true
		}
	}

	return false
}

// RetryOnTransientAPIError retries an operation when transient API errors occur.
// It uses exponential backoff with jitter to avoid thundering herd problems.
func RetryOnTransientAPIError(ctx context.Context, operation func() error) error {
	log := logr.FromContextOrDiscard(ctx)

	backoff := wait.Backoff{
		Steps:    5,                      // Maximum number of retries
		Duration: 500 * time.Millisecond, // Initial retry delay
		Factor:   2.0,                    // Exponential backoff factor
		Jitter:   0.1,                    // Add 10% jitter to avoid thundering herd
		Cap:      10 * time.Second,       // Maximum retry delay
	}

	var lastErr error
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		lastErr = operation()
		if lastErr == nil {
			return true, nil // Success
		}

		if isTransientAPIError(lastErr) {
			log.V(1).Info("transient API error encountered, will retry", "error", lastErr.Error())
			return false, nil // Retry
		}

		return false, lastErr // Non-transient error, stop retrying
	})

	if err != nil {
		if wait.Interrupted(err) {
			// Return the last error if we exhausted retries
			log.Error(lastErr, "exhausted retries for transient API error")
			return lastErr
		}
		return err
	}

	return nil
}

// ListWithRetry performs a List operation with retry logic for transient API errors.
func ListWithRetry(ctx context.Context, c client.Client, list client.ObjectList, opts ...client.ListOption) error {
	return RetryOnTransientAPIError(ctx, func() error {
		return c.List(ctx, list, opts...)
	})
}

// GetWithRetry performs a Get operation with retry logic for transient API errors.
func GetWithRetry(ctx context.Context, c client.Client, key client.ObjectKey, obj client.Object) error {
	return RetryOnTransientAPIError(ctx, func() error {
		return c.Get(ctx, key, obj)
	})
}

// UpdateWithRetry performs an Update operation with retry logic for both conflicts and transient API errors.
// It first attempts the update, and if it encounters a conflict, it refetches and merges changes.
// For transient API errors, it retries with exponential backoff.
func UpdateWithRetry(ctx context.Context, c client.Client, obj client.Object) error {
	return RetryOnTransientAPIError(ctx, func() error {
		return c.Update(ctx, obj)
	})
}
