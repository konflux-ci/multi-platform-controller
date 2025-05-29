package cloud

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateTaskRunID", func() {

	// Testing everything that should pass, according to ValidateTaskRunID's current code
	When("the TaskRunID is in the standard 'namespace:name' format", func() {
		It("should return no error", func() {
			input := "my-namespace:my-taskrun-123"
			err := ValidateTaskRunID(input)
			GinkgoWriter.Printf("Input: '%s' -> Expected: No Error, Got: %v\n", input, err)
			Expect(err).NotTo(HaveOccurred())
		})
	})
	// ValidateTaskRunID currently passes these, but shouldn't
	When("the TaskRunID is structurally valid, but semantically wrong (empty parts)", func() {
		DescribeTable("TaskRunID has one colon, but its actual parts are empty",
			func(input string, description string) {
				GinkgoWriter.Printf("Testing empty parts: Input: '%s' (Description: %s)\n", input, description)
				err := ValidateTaskRunID(input)
				GinkgoWriter.Printf("Input: '%s' -> Expected: No Error, Got: %v\n", input, err)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("empty namespace part", ":a-taskrun-name", "e.g., ':taskrun-name'"),
			Entry("empty name part", "a-namespace:", "e.g., 'namespace:'"),
			Entry("just a single colon", ":", "e.g., ':'"),
		)
	})

	When("the TaskRunID is structurally valid, but would be rejected by cloud provider' tagging rules", func() {
		DescribeTable("TaskRunID has one colon and breaks AWS or IBM Cloud's tag string rules",
			func(input string, cloudRejectionReason string) {
				GinkgoWriter.Printf(
					"Testing cloud provider rejection scenario: Input: '%s' (Reason for cloud rejection: %s)\n",
					input, cloudRejectionReason)
				err := ValidateTaskRunID(input)
				GinkgoWriter.Printf(
					"Input: '%s' -> Expected: No Error (from ValidateTaskRunID), Got: %v. This input would "+
						"likely be rejected by cloud provider.\n",
					input, err)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("AWS length violation (>256 chars)",
				strings.Repeat("a", 200)+":"+strings.Repeat("b", 57),
				"AWS tag values max 256 Unicode chars"),
			Entry("IBM length violation (>128 chars)",
				strings.Repeat("i", 70)+":"+strings.Repeat("j", 60),
				"IBM Cloud tags max 128 chars"),
			Entry("Illegal char for both AWS & IBM (e.g., '$')",
				"namespace:moshe$kipod",
				"Character '$' is not allowed in tags by neither AWS or IBM Cloud"),
		)
	})

	When("the TaskRunID is structurally valid, but contains an invalid Kubernetes namespace format", func() {
		DescribeTable("TaskRunID has one colon but does not seem to be coming from k8s",
			func(input string, reasonWhyK8sInvalid string) {
				GinkgoWriter.Printf(
					"Testing K8s invalid namespace scenario: Input: '%s' (Reason: %s)\n",
					input, reasonWhyK8sInvalid)
				err := ValidateTaskRunID(input)
				GinkgoWriter.Printf(
					"Input: '%s' -> Expected: No Error (for ValidateTaskRunID), Got: %v\n",
					input, err)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("namespace with Uppercase", "CamelCaseNamespace:task-name",
				"K8s namespaces must be lowercase"),
			Entry("namespace with underscore", "sssnake_ssspace_namessspacccce:task-name",
				"K8s namespaces do not allow underscores"),
			Entry("namespace ends with hyphen", "namespace-ends-with-hyphen-:task-name",
				"K8s namespaces cannot end with a hyphen"),
			Entry("namespace starts with hyphen", "-namespace-starts-with-hyphen:task-name",
				"K8s namespaces cannot start with a hyphen"),
			Entry("namespace with special char '$'", "a-$pecial-char-namespace:task-name",
				"K8s namespaces have restricted special characters"),
			Entry("namespace too long (64 chars)", strings.Repeat("a", 64)+":task-name",
				"K8s namespaces must be 63 characters or less"),
		)
	})
	// Testing what ValidateTaskRunID shouldn't pass according to its current code
	When("the TaskRunID fails the 'single colon' structural requirement", func() {
		DescribeTable("it should return a formatted error",
			func(input string, descriptionOfInvalidity string) {
				GinkgoWriter.Printf("Testing structurally invalid scenario: Input: '%s' (Reason: %s)\n",
					input, descriptionOfInvalidity)
				err := ValidateTaskRunID(input)
				Expect(err).To(HaveOccurred())

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
