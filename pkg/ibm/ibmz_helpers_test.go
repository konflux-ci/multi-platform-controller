package ibm

import (
	"fmt"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/testing/utils"
)

const testIterations = 100000
const maxAllowedErrors = 2

// expectedInstanceNameFormat verifies the instance name structure: sanitized-tag + "-" + 20-char-id + "x"
var expectedInstanceNameFormat = regexp.MustCompile(`^[a-z0-9-]+-[a-z0-9-]{20}x$`)

var _ = Describe("IBM s390x Helper Functions", func() {

	// A unit test for createInstanceName which serves both ibmz.go and ibmp.go use to create virtual machines.
	// Includes:
	// 	1. Happy-path tests to verify the logic of creating and sanitizing instance names.
	//	2. Validation tests to ensure createInstanceName rejects special characters.
	//	3. A uniqueness test to verify createInstanceName provides a unique-enough instance name within reasonable
	//	   repeats. This test also checks the robustness of createInstanceName's code to verify it does not fail more
	//	   than twice when run 100,000 times.
	Describe("The createInstanceName function", func() {
		When("provided with valid instanceTag inputs that should be sanitized", func() {
			DescribeTable("should successfully create and sanitize instance names",
				func(tag, description string) {
					name, _ := createInstanceName(tag)
					// Verify the name matches expected format: tag-{20 chars}x
					Expect(expectedInstanceNameFormat.MatchString(name)).To(BeTrue())
				},
				Entry("lowercase with hyphens", "test-tag", "valid lowercase with hyphens"),
				Entry("mixed case and underscores are sanitized", "Test_Instance_123", "mixed case and underscores sanitized"),
			)
		})

		When("provided with invalid instanceTag inputs", func() {
			DescribeTable("should reject the input with an error",
				func(tag, description string) {
					name, err := createInstanceName(tag)

					Expect(err).Should(MatchError(ContainSubstring("invalid characters")))
					Expect(name).Should(BeEmpty())
				},
				Entry("special characters rejected", "moshe_kipod_Funky-Tag*!@#", "contains multiple invalid characters"),
				Entry("empty string rejected", "", "empty tag"),
				Entry("just a space tag", " ", "space tag"),
				Entry("spaces rejected", "test tag", "contains space character"),
			)
		})

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
