// Assisted-by: TAG
package main

import (
	"fmt"

	"github.com/konflux-ci/multi-platform-controller/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const testRounds = 100000
const maxAllowedErrorsForRandomString = 0

var _ = Describe("OTP unit tests", func() {

	// A unit test for GenerateRandomString. Runs GenerateRandomString 10,000 times and checks if any
	// of them has been generatd before to validate randomness. This test takes 1.6 seconds but starts
	// to slow down to > 10 seconds at 50,000.
	// I think 10,000 runs is convincing enough...
	Describe("Testing GenerateRandomString", func() {

		It("GenerateRandomString should generate random strings without errors", func() {

			generator := func() (string, error) {
				return GenerateRandomString(20)
			}

			stats := utils.PerformUniquenessAndPerformanceTest(testRounds, generator)

			GinkgoWriter.Printf(
				"Performance Report for GenerateRandomString:\n"+
					"  Target Iterations: %d\n"+
					"  Successfully Generated: %d\n"+
					"  Unique Strings: %d\n"+
					"  Duplicate Strings Found: %d\n"+
					"  Errors Encountered: %d\n"+
					"  Total Duration: %v\n",
				testRounds,
				stats.GeneratedCount,
				stats.UniqueCount,
				stats.DuplicateCount,
				stats.ErrorCount,
				stats.ActualDuration,
			)

			Expect(stats.ErrorCount).Should(Equal(maxAllowedErrorsForRandomString),
				fmt.Sprintf("GenerateRandomString: Expected no more than %d errors, but got %d",
					maxAllowedErrorsForRandomString, stats.ErrorCount))
			Expect(stats.DuplicateCount).Should(Equal(0),
				fmt.Sprintf("GenerateRandomString: Expected 0 duplicate strings, but found %d",
					stats.DuplicateCount))
		})
	})

})
