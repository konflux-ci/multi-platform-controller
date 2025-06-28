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

			It("should not have a data race", func() {
				ctx, cancel := context.WithCancel(context.Background())
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

				// Persistent writer
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

				time.Sleep(200 * time.Millisecond)

				cancel()
				wg.Wait()
			})
		})
	})
})
