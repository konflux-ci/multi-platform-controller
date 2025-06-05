package ibm

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"

	"github.com/konflux-ci/multi-platform-controller/testing/utils"
)

const testIterations = 100000
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

		When("provided with an instanceTag containing mixed-cases or non-IBM-friendly characters", func() {
			const problematicTag = "moshe_kipod_Funky-Tag*!@#"
			name, err := createInstanceName(problematicTag)

			It("should currently prepend the tag as-is without sanitization", func() {
				Expect(err).ShouldNot(HaveOccurred())

				expectedPrefix := problematicTag + "-"
				Expect(name).Should(HavePrefix(expectedPrefix))

				GinkgoWriter.Printf("Input instanceTag: %s, generated name: %s\n", problematicTag, name)
				Expect(name).ShouldNot(MatchRegexp("^[a-z0-9-]+$"),
					"IBM instance names can only contain lowercase alphanumeric characters and hyphens")
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
