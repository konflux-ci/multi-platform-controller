package taskrun

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// A helper to create a TaskRun with a platform parameter.
func createTrWithPlatform(platform string) *pipelinev1.TaskRun {
	tr := &pipelinev1.TaskRun{
		Spec: pipelinev1.TaskRunSpec{
			Params: []pipelinev1.Param{
				{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues(platform)},
			},
		},
	}
	return tr
}

var _ = Describe("Platform Validation Tests", func() {

	// This section tests the utility function responsible for extracting the
	// target platform from a TaskRun's parameters.
	Describe("Test extractPlatform function", func() {
		It("should extract platform from TaskRun parameters successfully", func() {
			tr := &pipelinev1.TaskRun{
				Spec: pipelinev1.TaskRunSpec{
					Params: []pipelinev1.Param{
						{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues("linux/amd64")},
					},
				},
			}

			platform, err := extractPlatform(tr)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(platform).Should(Equal("linux/amd64"))
		})

		It("should return error when PlatformParam parameter is missing", func() {
			tr := &pipelinev1.TaskRun{
				Spec: pipelinev1.TaskRunSpec{
					Params: []pipelinev1.Param{
						{Name: "OTHER_PARAM", Value: *pipelinev1.NewStructuredValues("other_value")},
					},
				},
			}

			_, err := extractPlatform(tr)
			Expect(err).Should(MatchError(errMissingPlatformParameter))
		})
	})

	// This section provides thorough unit tests for the platform format validation function.
	// It is structured to test happy paths, sad paths, and special exceptions distinctly.
	Describe("The validatePlatformFormat function", func() {

		When("validating a correctly formatted platform string", func() {
			DescribeTable("it should not return an error",
				func(platform string) {
					err := validatePlatformFormat(platform)
					Expect(err).ShouldNot(HaveOccurred())
				},
				Entry("for linux/amd64", "linux/amd64"),
				Entry("for linux/s390x", "linux/s390x"),
			)
		})

		When("validating a special exception platform string", func() {
			DescribeTable("it should not return an error",
				func(platform string) {
					err := validatePlatformFormat(platform)
					Expect(err).ShouldNot(HaveOccurred())
				},
				Entry("for 'local'", "local"),
				Entry("for 'localhost'", "localhost"),
				Entry("for 'linux/x86_64'", "linux/x86_64"),
			)
		})

		When("validating a malformed platform string", func() {
			// A helper variable for the length test to keep the table entry clean.
			longLabel := strings.Repeat("a", 64)

			DescribeTable("it should return an invalid format error",
				func(platform string) {
					err := validatePlatformFormat(platform)
					Expect(err).Should(MatchError(errInvalidPlatformFormat))
				},
				// --- Structural Errors ---
				Entry("because it has too few parts", "linux"),
				Entry("because it has too many parts", "linux/amd64/extra"),
				Entry("because the first part is empty", "/amd64"),
				Entry("because the second part is empty", "linux/"),
				Entry("because it's just an empty string", ""),
				Entry("because it has a trailing slash", "linux/amd64/"),
				// --- DNS-1035 Label Violations ---
				Entry("because it contains uppercase characters", "linux/AMD64"),
				Entry("because it contains an underscore", "linux/amd_64"),
				Entry("because a part starts with a hyphen", "-linux/amd64"),
				Entry("because a part ends with a hyphen", "linux/amd64-"),
				Entry("because a part is longer than 63 chars", fmt.Sprintf("linux/%s", longLabel)),
			)
		})
	})

	// This section tests the combined validation function
	Describe("The validatePlatform function", func() {

		When("the TaskRun contains a valid platform parameter", func() {
			DescribeTable("it should return the platform and no error",
				func(platformValue string) {
					tr := createTrWithPlatform(platformValue)
					platform, err := validatePlatform(tr)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(platform).Should(Equal(platformValue))
				},
				Entry("for a standard platform", "linux/amd64"),
				Entry("for the 'linux/x86_64' exception", "linux/x86_64"),
			)
		})

		When("the TaskRun contains an invalid or missing platform parameter", func() {
			It("should return error when platform parameter is missing", func() {
				tr := &pipelinev1.TaskRun{
					Spec: pipelinev1.TaskRunSpec{
						Params: []pipelinev1.Param{},
					},
				}
				_, err := validatePlatform(tr)
				Expect(err).Should(MatchError(errMissingPlatformParameter))
			})

			It("should return error when platform parameter format is invalid", func() {
				tr := createTrWithPlatform("koko_hazamar/moshe_ata_lo_kipod")
				_, err := validatePlatform(tr)
				Expect(err).Should(MatchError(errInvalidPlatformFormat))
			})
		})
	})
})
