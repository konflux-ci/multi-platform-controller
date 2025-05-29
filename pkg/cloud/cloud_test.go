package cloud

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A unit test for ValidateTaskRunID. For now only tests input that should not return an error from ValidateTaskRunID
// and input that should return an error code, according to ValidateTaskRunID's current logic.
// More testing scenarios are available, but they're currently exposing ValidateTaskRunID's bugs so are on hiatus and
// the bug is tracked on Jira (ticket number inside the test spec's body).
var _ = Describe("ValidateTaskRunID", func() {

	// Testing everything that should pass, according to ValidateTaskRunID's current code
	When("the TaskRunID is in the standard 'namespace:name' format", func() {
		It("should return no error", func() {
			input := "my-namespace:my-taskrun-123"
			err := ValidateTaskRunID(input)
			GinkgoWriter.Printf("Input: '%s' -> Expected: No Error, Got: %v\n", input, err)
			Expect(err).Should(BeNil())
		})
	})

	// When KFLUXINFRA-1697 is fixed, there will be more test scenarios here

	// Testing what ValidateTaskRunID shouldn't pass according to its current code
	When("the TaskRunID fails the 'single colon' structural requirement", func() {
		DescribeTable("it should return a formatted error",
			func(input string, descriptionOfInvalidity string) {
				GinkgoWriter.Printf("Testing structurally invalid scenario: Input: '%s' (Reason: %s)\n",
					input, descriptionOfInvalidity)
				err := ValidateTaskRunID(input)
				Expect(err).Should(HaveOccurred())

				expectedErrorMessage := fmt.Sprintf(
					"'%s' does not follow the correct format: '<TaskRun Namespace>:<TaskRun Name>'", input)
				Expect(err.Error()).To(Equal(expectedErrorMessage))
			},
			Entry("no colons", "nocolonshere", "Zero colons"),
			Entry("empty string", "", "Empty string, zero colons"),
			Entry("single space string", " ", "Single space, zero colons"),
			Entry("too many colons", "too:many:colons:in:this:id", "Multiple colons"),
			Entry("only colons", ":::", "Multiple colons, no other content"),
		)
	})
})
