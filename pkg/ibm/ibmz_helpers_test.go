package ibm

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/testing/utils"
)

const testIterations = 100000
const maxAllowedErrors = 2

var _ = Describe("IBM s390x Helper Functions", func() {

	// A unit test for createInstanceName which serves both ibmz.go and ibmp.go use to create virtual machines.
	// Includes:
	// 	1. Happy-path tests to verify the logic of creating and sanitizing instance names.
	//	2. Validation tests to ensure createInstanceName rejects special characters.
	//	3. A uniqueness test to verify createInstanceName provides a unique-enough instance name within reasonable
	//	   repeats. This test also checks the robustness of createInstanceName's code to verify it does not fail more
	//	   than twice when run 100,000 times.
	Describe("The createInstanceName function", func() {
		DescribeTable("sanitization and validation of instanceTag input",
			func(tag, description string, shouldError bool) {
				name, err := createInstanceName(tag)

				if shouldError {
					Expect(err).Should(HaveOccurred())
					Expect(err.Error()).Should(ContainSubstring("invalid characters"))
					Expect(name).Should(BeEmpty())
				} else {
					Expect(err).ShouldNot(HaveOccurred())
					// Verify the entire generated name matches IBM naming rules
					Expect(name).Should(MatchRegexp("^[a-z0-9-]+x$"),
						"The entire instance name should contain only lowercase alphanumeric characters and hyphens, ending with 'x'")
				}
			},
			Entry("lowercase with hyphens", "test-tag", "valid lowercase with hyphens", false),
			Entry("uppercase letters are sanitized", "MyInstanceTag", "uppercase converted to lowercase", false),
			Entry("underscores are sanitized", "my_test_instance", "underscores replaced with hyphens", false),
			Entry("mixed case and underscores are sanitized", "Test_Instance_123", "mixed case and underscores sanitized", false),
			Entry("special characters rejected", "moshe_kipod_Funky-Tag*!@#", "contains multiple invalid characters", true),
			Entry("empty string rejected", "", "empty tag", true),
		)

		When("generating a large batch of 100,000 instance names with the same tag", func() {
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
