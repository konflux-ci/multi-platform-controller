package mpcmetrics

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This test suite covers the only testable function in backend_probe.go - CheckAvailability.
// Is split into two main sections:
//  1. Testing the logic of returning nothing (successful platform) or returning an error (failing platform). This is
//     not only tested by creating conditions for a successful/failing platform, but also a boundary test for the
//     errorThreshold constant that dynamically evaluates the boundaries to test for future robustness.
//  2. Test the state change of a platform as CheckAvailability is called twice, verifying the Swap() action clears all
//     successes and failures of a probe.
var _ = Describe("Backend_Probe CheckAvailability unit tests", func() {

	var (
		probe AvailabilityProbe
		ctx   context.Context
	)

	BeforeEach(func() {
		ctx = context.TODO()
	})

	// Testing the logic determining whether a platform is successful or failing
	When("the probe is healthy", func() {
		It("should pass with no successes and no failures", func() {
			probe = setupProbeWithCounts(0, 0)
			err := probe.CheckAvailability(ctx)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("should pass with no successes and only one failure", func() {
			probe = setupProbeWithCounts(0, 1)
			err := probe.CheckAvailability(ctx)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("should pass with many successes and no failures", func() {
			probe = setupProbeWithCounts(20, 0)
			err := probe.CheckAvailability(ctx)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("should pass with a failure rate clearly below the threshold", func() {
			probe = setupProbeWithCounts(10, 1) // success/failures ratio - 0.1
			err := probe.CheckAvailability(ctx)
			Expect(err).ShouldNot(HaveOccurred())
		})
	})

	When("the probe is unhealthy", func() {
		It("should fail with zero successes and multiple failures", func() {
			probe = setupProbeWithCounts(0, 2)
			err := probe.CheckAvailability(ctx)
			Expect(err).Should(HaveOccurred())
		})

		It("should fail with a failure rate clearly above the threshold", func() {
			probe = setupProbeWithCounts(10, 9) // success/failures ratio - 0.9
			err := probe.CheckAvailability(ctx)
			Expect(err).Should(HaveOccurred())
		})
	})

	When("testing errorThreshold boundary conditions", func() {

		baseSuccesses := 100
		// A count of failures that should result in a ratio == errorThreshold
		failuresAtEdge := int(float64(baseSuccesses) * errorThreshold)

		// Below are calculations written with the assumption that errorThreshold will never be less than 0.1, because
		// it's a less realistic situation in production. It also feels like a threshold that's way too accurate
		// for the general coding spirit of the team - if it's so small, why not just make failures completely
		// intolerable and be done with it.
		failuresToPass := failuresAtEdge - 1 // A count of failures that should result in a ratio < errorThreshold
		failuresToFail := failuresAtEdge + 1 // A count of failures that should result in a ratio > errorThreshold

		It("should pass when the failure rate is just below the threshold", func() {
			probe := setupProbeWithCounts(baseSuccesses, failuresToPass)
			err := probe.CheckAvailability(ctx)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("should pass when the failure rate is exactly at the threshold", func() {
			probe := setupProbeWithCounts(baseSuccesses, failuresAtEdge)
			err := probe.CheckAvailability(ctx)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("should fail when the failure rate is just over the threshold", func() {
			probe := setupProbeWithCounts(baseSuccesses, failuresToFail)
			err := probe.CheckAvailability(ctx)
			Expect(err).Should(HaveOccurred())
		})
	})

	// State change test
	When("its state is checked sequentially", func() {
		It("should reset its counters, resulting in a healthy status on the subsequent check", func() {
			By("1. Setting up an unhealthy state")
			probe = setupProbeWithCounts(0, 5)

			By("2. Verifying the first check fails as expected")
			err1 := probe.CheckAvailability(ctx)
			Expect(err1).Should(HaveOccurred())

			By("3. Verifying the second check passes because counters were reset")
			// The probe now has 0 successes and 0 failures internally because they've been Swap-ped
			err2 := probe.CheckAvailability(ctx)
			Expect(err2).ShouldNot(HaveOccurred(),
				"The second check should pass because the counters should have been reset to zero")
		})
	})
})

// A helper that creates a new BackendProbe and populates it with a specific number of success and failure events.
func setupProbeWithCounts(successesCount, failuresCount int) AvailabilityProbe {
	p := NewBackendProbe()
	for i := 0; i < successesCount; i++ {
		p.Success()
	}
	for i := 0; i < failuresCount; i++ {
		p.Failure()
	}
	return p
}
