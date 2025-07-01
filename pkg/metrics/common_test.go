package mpcmetrics

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Common functions unit tests", func() {

	// A unit test for the possibility of race conditions hitting the functions in common.go that handle the probes map.
	// This test is designed to reliably trigger race conditions via goroutines as persistent readers and writers
	// to the probes map, running for a controlled duration.
	// This test's meaning of `passing` is `nothing panicked because of race conditions on probes`.
	Describe("common.go race condition test", func() {

		Context("when the probes map is accessed concurrently", func() {
			BeforeEach(func() {
				probes = make(map[string]*AvailabilityProbe)
			})

			It("should not have a data race", func(tctx context.Context) {
				ctx, cancel := context.WithCancel(tctx)
				// This cancel() is a safety net to ensures that if the test ever panics or returns early for any
				// possible reason, the context is always guaranteed to be cleaned up and prevent goroutine leaks.
				defer cancel()

				var wg sync.WaitGroup
				writerCount := 5

				// Persistent reader
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					for {
						select {
						case <-ctx.Done():
							return
						default:
							checkProbes(ctx)
						}
					}
				}()

				// Persistent writers - these writers continuously attempt to add new entries to probes, which is the
				// "write" part of the race condition created in the test.
				for i := 0; i < writerCount; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer GinkgoRecover()
						for {
							select {
							case <-ctx.Done():
								return
							default:
								randomPlatform := fmt.Sprintf("platform-%d", rand.Intn(100))
								CountAvailabilitySuccess(randomPlatform)
								CountAvailabilityError(randomPlatform)
							}
						}
					}()
				}
				// Allow the concurrent goroutine writers to run for a set duration, creating the necessary window for
				// a data race to occur between themselves.
				time.Sleep(200 * time.Millisecond)

				// Explicitly signals all goroutine writers to stop their work. This is critical for a graceful shutdown
				// before we wait for them so that the wg.Wait() call can complete.
				cancel()
				// A synchronization point. It blocks until all goroutines have received the cancellation signal and
				// called wg.Done().
				wg.Wait()
			})
		})
	})
})
