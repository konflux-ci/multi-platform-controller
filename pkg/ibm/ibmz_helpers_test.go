package ibm

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/testing/utils"
)

const testIterations = 100000
const maxAllowedErrors = 2

var _ = Describe("IBM s390x Helper Functions", func() {

	// A unit test for createInstanceName which serves both ibmz.go and ibmp.go use to create virtual machines.
	// Includes:
	// 	1. A simple happy-path test to verify the logic of creating an instance name.
	//	2. A uniqueness test to verify createInstanceName provides a unique-enough instance name within reasonable
	//	   repeats. This test also checks the robustness of createInstanceName's code to verify it does not fail more
	//	   than twice when run 10,000 times.
	// These test cases were chosen because there's not much more room to try and break or abuse this function:
	// Enforcing legal characters is not necessarily createInstanceName's responsibility. An instance name beginning
	// with a hyphen is perfectly legal in IBM Cloud so an empty instanceTag input are edge cases that are not relevant.
	// Trying to make createInstanceName return an error involves heavy mocking of a golang package, and downstream
	// from createInstanceName there's a tolerance of up to 2 failed instance creation fails so there isn't too much
	// point in forcing this heavy-duty test.
	Describe("The createInstanceName function", func() {
		When("generating a single instance name with a given tag", func() {
			const instanceTag = "test-tag"

			It("should return a name in the correct format without any errors", func() {
				name, err := createInstanceName(instanceTag)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(name).Should(HavePrefix(instanceTag + "-"))
				Expect(name).Should(HaveSuffix("x"))

				// Extract the ID part: tag-IDx, then shave off the "x" suffix
				parts := strings.SplitN(name, "-", 2) // Split on the first hyphen
				Expect(parts).Should(HaveLen(2), "Name should contain the tag and the ID part separated by a hyphen")
				idPart := parts[1][:20]
				Expect(idPart).Should(MatchRegexp("^[a-z0-9-]+$"),
					"ID part should consist of lowercase alphanumeric characters and hyphens")
			})
		})

		When("generating a large batch of 10,000 instance names with the same tag", func() {
			var instanceTagForBatch = "tag-uniqueness-tag"

			It("should produce unique names each time and encounter no more than two errors in the process", func() {

				generator := func() (string, error) {
					return createInstanceName(instanceTagForBatch)
				}

				stats := utils.PerformUniquenessAndPerformanceTest(testIterations, generator)

				GinkgoWriter.Printf(
					"Performance Report for createInstanceName:\n"+
						"  Target Iterations: %d\n"+
						"  Successfully Generated: %d\n"+
						"  Unique Names: %d\n"+
						"  Duplicate Names Found: %d\n"+
						"  Errors Encountered: %d\n"+
						"  Total Duration: %v\n",
					testIterations,
					stats.GeneratedCount,
					stats.UniqueCount,
					stats.DuplicateCount,
					stats.ErrorCount,
					stats.ActualDuration,
				)

				Expect(stats.ErrorCount).Should(BeNumerically("<", maxAllowedErrors),
					fmt.Sprintf("Expected no more than %d errors, but got %d",
						maxAllowedErrors, stats.ErrorCount))
				Expect(stats.DuplicateCount).Should(Equal(0),
					fmt.Sprintf("Expected 0 duplicate names, but found %d",
						stats.DuplicateCount))
			})
		})
	})
})
