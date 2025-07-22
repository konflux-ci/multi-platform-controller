package cloud

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
)

// A unit test for ValidateTaskRunID. Checks for a simple valid TaskRunID that should pass, and has a large table-driven
// test checking the various aspects of the validation logic - constraints from individual cloud providers, constraints
// on namespace names in Kubernetes etc. The invalidation tests not only verify that an error occurred but that the
// error message accurately reflects what failed the TaskRunID.
var _ = Describe("ValidateTaskRunID", func() {

	When("the TaskRunID is valid", func() {
		It("should not return an error for a standard 'namespace:name' format", func() {
			err := ValidateTaskRunID("my-namespace:my-taskrun-123")
			Expect(err).ShouldNot(HaveOccurred())
		})
	})

	When("the TaskRunID is invalid", func() {
		DescribeTable("it should return a descriptive error",
			func(input string, expectedErrSubstring string) {
				Expect(ValidateTaskRunID(input)).To(MatchError(ContainSubstring(expectedErrSubstring)))
			},
			// Structural format errors
			Entry("when the string is empty", "", "neither the namespace nor the name can be empty"),
			Entry("when there are no colons", "nocolonshere", "does not follow the correct format"),
			Entry("when there are too many colons", "too:many:colons", "does not follow the correct format"),
			Entry("when the namespace part is empty", ":a-taskrun-name", "neither the namespace nor the name can be empty"),
			Entry("when the name part is empty", "a-namespace:", "neither the namespace nor the name can be empty"),
			Entry("when it's just a colon", ":", "neither the namespace nor the name can be empty"),

			// Cloud provider rule violations
			Entry("when the tag is too long (>128 chars)", strings.Repeat("i", 70)+":"+strings.Repeat("j", 60), "cannot be longer than 128 chars"),
			Entry("when the tag has 'aws:' prefix", "aws:my-task", "cannot start with 'aws:'"),
			Entry("when the tag has 'AWS:' prefix (case-insensitive)", "AWS:my-task", "cannot start with 'aws:'"),

			// Kubernetes RFC 1123 violations
			Entry("when the namespace is too long (>63 chars)", strings.Repeat("a", 64)+":task-name", "is too long for a Kubernetes label"),
			Entry("when the namespace contains an illegal character '$'", "moshe$kipod:my-task", "is not a valid RFC 1123 label"),
			Entry("when the namespace contains uppercase characters", "CamelCaseNamespace:my-task", "is not a valid RFC 1123 label"),
			Entry("when the namespace starts with a hyphen", "-namespace:my-task", "is not a valid RFC 1123 label"),
			Entry("when the namespace ends with a hyphen", "namespace-:my-task", "is not a valid RFC 1123 label"),
		)
	})
})
