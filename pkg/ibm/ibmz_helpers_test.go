package ibm

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"

	"github.com/konflux-ci/multi-platform-controller/testing/utils"
)

const testIterations = 10000000
const maxAllowedErrors = 2

var _ = Describe("IBM s390x Helper Functions", func() {

	Describe("The createInstanceName function", func() {
		When("generating a single instance name with a given tag", func() {
			const instanceTag = "test-tag"
			name, err := createInstanceName(instanceTag)

			It("should return a name in the correct format without any errors", func() {
				Expect(err).ShouldNot(HaveOccurred())
				Expect(name).Should(HavePrefix(instanceTag + "-"))
				Expect(name).Should(HaveSuffix("x"))

				// Extract the ID part: tag-IDx, then shave off the "x" suffix
				parts := strings.SplitN(name, "-", 2) // Split on the first hyphen
				Expect(parts).Should(HaveLen(2), "Name should contain the tag and the ID part separated by a hyphen")
				idPart := parts[1][:20]

				Expect(idPart).Should(HaveLen(20),
					"ID part itself should be 20 characters long")
				Expect(idPart).Should(Equal(strings.ToLower(idPart)),
					"ID part should be in lowercase")
				Expect(idPart).ShouldNot(ContainSubstring("_"),
					"ID part should not contain underscores")
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

	Describe("The createInstanceName function's output when truncated by callers", func() {

		When("its output (using a 30-char tag) is truncated to 47 characters, simulating PowerPC naming constraints", func() {
			const tagNamePPC = "powerpc-platform-instance-prefix-123456" // Length 36
			const maxLengthPPC = 47

			It("should be evaluated for an increased chance of name collisions over 10,000 generations", func() {
				ppcNameGenerator := func() (string, error) {
					fullName, err := createInstanceName(tagNamePPC)
					if err != nil {
						return "", err
					}
					if len(fullName) > maxLengthPPC {
						return fullName[:maxLengthPPC], nil
					}
					return fullName, nil
				}

				stats := utils.PerformUniquenessAndPerformanceTest(testIterations, ppcNameGenerator)

				GinkgoWriter.Printf(
					"\n--- Truncation Test Report (PowerPC Naming Scenario) ---\n"+
						"  Tag used: %q (length %d)\n"+
						"  Original Name Length (approx): %d\n"+
						"  Truncated Name Length: %d\n"+
						"  Effective ID Length for Uniqueness: %d (target was 20)\n"+
						"  Target Iterations: %d\n"+
						"  Successfully Generated & Truncated: %d\n"+
						"  Unique Names After Truncation: %d\n"+
						"  Duplicate Names Found: %d\n"+
						"  Errors During Generation: %d\n"+
						"  Total Duration: %v\n"+
						"--- End Report ---\n",
					tagNamePPC, len(tagNamePPC),
					len(tagNamePPC)+1+20+1,
					maxLengthPPC,
					maxLengthPPC-len(tagNamePPC)-1,
					testIterations,
					stats.GeneratedCount,
					stats.UniqueCount,
					stats.DuplicateCount,
					stats.ErrorCount,
					stats.ActualDuration,
				)

				Expect(stats.ErrorCount).Should(BeNumerically("<", maxAllowedErrors),
					"Errors during generation should be within limits")
				Expect(stats.DuplicateCount).Should(Equal(0),
					"Expected 0 duplicate names even after 47-char truncation for 10k runs")
			})
		})

		When("its output (using a 45-char tag) is truncated to 63 characters, simulating System Z naming constraints", func() {
			const tagNameS390 = "systemz-enterprise-linux-server-instance-tag-123456789" // Length 54
			const maxLengthS390 = 63

			It("should be evaluated for an increased chance of name collisions over 10,000 generations", func() {
				s390NameGenerator := func() (string, error) {
					fullName, err := createInstanceName(tagNameS390)
					if err != nil {
						return "", err
					}
					if len(fullName) > maxLengthS390 {
						return fullName[:maxLengthS390], nil
					}
					return fullName, nil
				}

				stats := utils.PerformUniquenessAndPerformanceTest(testIterations, s390NameGenerator)

				GinkgoWriter.Printf(
					"\n--- Truncation Test Report (System Z Naming Scenario) ---\n"+
						"  Tag used: %q (length %d)\n"+
						"  Original Name Length (approx): %d\n"+
						"  Truncated Name Length: %d\n"+
						"  Effective ID Length for Uniqueness: %d (target was 20)\n"+
						"  Target Iterations: %d\n"+
						"  Successfully Generated & Truncated: %d\n"+
						"  Unique Names After Truncation: %d\n"+
						"  Duplicate Names Found: %d\n"+
						"  Errors During Generation: %d\n"+
						"  Total Duration: %v\n"+
						"--- End Report ---\n",
					tagNameS390, len(tagNameS390),
					len(tagNameS390)+1+20+1,
					maxLengthS390,
					maxLengthS390-len(tagNameS390)-1,
					testIterations,
					stats.GeneratedCount,
					stats.UniqueCount,
					stats.DuplicateCount,
					stats.ErrorCount,
					stats.ActualDuration,
				)

				Expect(stats.ErrorCount).Should(BeNumerically("<", maxAllowedErrors),
					"Errors during generation should be within limits")
				Expect(stats.DuplicateCount).Should(Equal(0),
					"Expected 0 duplicate names even after 63-char truncation for 10k runs")
			})
		})
	})

})
