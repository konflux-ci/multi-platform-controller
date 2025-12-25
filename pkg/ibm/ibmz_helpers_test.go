package ibm

import (
	"fmt"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/multi-platform-controller/testing/utils"
)

const testIterations = 10000000
const maxAllowedErrors = 2

// expectedInstanceNameFormat verifies the instance name structure: sanitized-tag + "-" + 16-hex-hash + "x"
// All instance names use a fixed 16-character hash
var expectedInstanceNameFormat = regexp.MustCompile(`^[a-z0-9-]+-[0-9a-f]{16}x$`)

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
				func(tag string) {
					name, _ := createInstanceName(tag)
					// Verify the name matches expected format: tag-{16-hex-hash}x
					Expect(expectedInstanceNameFormat.MatchString(name)).To(BeTrue())
				},
				Entry("lowercase with hyphens", "test-tag"),
				Entry("mixed case and underscores are sanitized", "Test_Instance_123"),
			)
		})

		When("provided with invalid instanceTag inputs", func() {
			DescribeTable("should reject the input with an error",
				func(tag string) {
					name, err := createInstanceName(tag)

					Expect(err).Should(MatchError(ContainSubstring("invalid characters")))
					Expect(name).Should(BeEmpty())
				},
				Entry("special characters rejected", "moshe_kipod_Funky-Tag*!@#"),
				Entry("empty string rejected", ""),
				Entry("just a space tag", " "),
				Entry("spaces rejected", "test tag"),
				Entry("space in the beginning of the tag rejected", " test-tag"),
				Entry("tag name starting with hyphen rejected", "-koko-hazamar"),
			)
		})

		DescribeTable("should produce unique names with 16-char hash",
			func(tag string, scenarioName string) {
				generator := func() (string, error) {
					name, err := createInstanceName(tag)
					return name, err
				}

				stats := utils.PerformUniquenessAndPerformanceTest(testIterations, generator)

				GinkgoWriter.Printf(
					"\nPerformance Report for createInstanceName (%s):\n"+
						"  Tag used: %q (length %d)\n"+
						"  Target Iterations: %d\n"+
						"  Successfully Generated: %d\n"+
						"  Unique Names: %d\n"+
						"  Duplicate Names Found: %d\n"+
						"  Errors Encountered: %d\n"+
						"  Total Duration: %v\n",
					scenarioName,
					tag, len(tag),
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
					fmt.Sprintf("Expected 0 duplicate names, but found %d duplicates",
						stats.DuplicateCount))
			},
			Entry("very short tag",
				"test", // 4 chars - minimal tag
				"Very Short Tag"),
			Entry("PowerPC: max validation length",
				"prod-ppc64le-at-max-limit-29", // 28 chars - at 29 char validation limit
				"PowerPC Max Tag Length"),
			Entry("System Z: max validation length",
				"prod-s390x-enterprise-tag-at-validation-max45", // 45 chars - at 45 char validation limit
				"System Z Max Tag Length"),
		)
	})
})
