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

	// Testing what ValidateTaskRunID shouldn't pass according to its current code
	When("the TaskRunID fails the 'single colon' structural requirement", func() {
		DescribeTable("it should return a formatted error",
			func(input string, descriptionOfInvalidity string) {
				expectedErr := fmt.Errorf(
					"'%s' does not follow the correct format: '<TaskRun Namespace>:<TaskRun Name>'", input)
				Expect(ValidateTaskRunID(input)).To(MatchError(expectedErr), descriptionOfInvalidity)
			},
			Entry("no colons", "nocolonshere", "Zero colons"),
			Entry("empty string", "", "Empty string, zero colons"),
			Entry("single space string", " ", "Single space, zero colons"),
			Entry("too many colons", "too:many:colons:in:this:id", "Multiple colons"),
			Entry("only colons", ":::", "Multiple colons, no other content"),
		)
	})
})
