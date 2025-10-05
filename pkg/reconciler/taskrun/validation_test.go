// This file contains tests for the validation functions used by the TaskRun reconciler.
// It covers platform format validation, numeric parameter validation (instance counts, timeouts),
// IP address validation and obfuscation, IBM host secret validation, and dynamic instance tag
// parsing and validation for AWS EC2 configurations.
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

var _ = Describe("Host Configuration Validation Tests", func() {

	// This section tests the utility function responsible for extracting the
	// target platform from a TaskRun's parameters.
	Describe("The extractPlatform function", func() {

		When("extracting platform from TaskRun parameters", func() {
			It("should extract platform from TaskRun parameters successfully", func() {
				tr := createTrWithPlatform("linux/amd64")
				Expect(extractPlatform(tr)).To(Equal("linux/amd64"))
			})

			It("should extract platform from TaskRun with multiple parameters", func() {
				tr := &pipelinev1.TaskRun{
					Spec: pipelinev1.TaskRunSpec{
						Params: []pipelinev1.Param{
							{Name: "OTHER_PARAM", Value: *pipelinev1.NewStructuredValues("other_value")},
							{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues("linux/arm64")},
							{Name: "ANOTHER_PARAM", Value: *pipelinev1.NewStructuredValues("another_value")},
						},
					},
				}
				Expect(extractPlatform(tr)).To(Equal("linux/arm64"))
			})
		})

		When("handling edge cases with platform parameters", func() {
			It("should return first occurrence when multiple PlatformParam parameters exist", func() {
				tr := &pipelinev1.TaskRun{
					Spec: pipelinev1.TaskRunSpec{
						Params: []pipelinev1.Param{
							{Name: "OTHER_PARAM", Value: *pipelinev1.NewStructuredValues("other_value")},
							{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues("linux/amd64")},
							{Name: "MIDDLE_PARAM", Value: *pipelinev1.NewStructuredValues("middle_value")},
							{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues("linux/arm64")},
							{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues("linux/s390x")},
						},
					},
				}
				Expect(extractPlatform(tr)).To(Equal("linux/amd64")) // Should return the first occurrence
			})
		})

		When("the PlatformParam parameter is missing", func() {
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
						Expect(validatePlatform(tr)).To(Equal(platformValue))
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
					Expect(validatePlatform(tr)).Error().To(MatchError(errMissingPlatformParameter))
				})

				It("should return error when platform parameter format is invalid", func() {
					tr := createTrWithPlatform("koko_hazamar/moshe_ata_lo_kipod")
					_, err := validatePlatform(tr)
					Expect(err).Should(MatchError(errInvalidPlatformFormat))
				})
			})
		})
	})

	// This section verifies validation of the numeric values that appear in host configurations.
	Describe("The validateNumericValue function", func() {

		When("validating numeric values within valid range", func() {
			DescribeTable("it should return the parsed integer value",
				func(value string, maxValue int, expected int) {
					Expect(validateNumericValue(value, maxValue)).Should(Equal(expected))
				},
				Entry("with minimum value 0", "0", 100, 0),
				Entry("with mid-range value", "50", 100, 50),
				Entry("with maximum value", "100", 100, 100),
				Entry("with very large maximum value", "1000", 1000000000, 1000),
			)
		})

		When("validating numeric values outside valid range or invalid format", func() {
			DescribeTable("it should return an error",
				func(value string, maxValue int) {
					_, err := validateNumericValue(value, maxValue)
					Expect(err).Should(MatchError(errInvalidNumericValue))
				},
				Entry("with negative value", "-1", 100),
				Entry("with value exceeding maximum", "101", 100),
				Entry("with value far exceeding maximum", "100000", 100),
				Entry("with non-numeric string", "abc", 100),
				Entry("with empty string", "", 100),
				Entry("with decimal value", "50.5", 100),
				Entry("with value containing spaces", " 50 ", 100),
			)
		})
	})

	// This section verifies validation of the max-instances parameter for dynamic host allocation.
	Describe("The validateMaxInstances function", func() {

		When("validating max-instances values", func() {
			DescribeTable("should accept valid instance count within range",
				func(value string, expected int) {
					Expect(validateMaxInstances(value)).Should(Equal(expected))
				},
				Entry("with instance count within range value 10", "10", 10),
				Entry("with maximum allowed instances value", "250", 250),
			)
		})
	})

	// This section verifies validation of the allocation timeout parameter for host provisioning.
	Describe("The validateMaxAllocationTimeout function", func() {

		When("validating allocation timeout values values", func() {
			DescribeTable("should accept valid timeout within range",
				func(value string, expected int) {
					Expect(validateMaxAllocationTimeout(value)).Should(Equal(expected))
				},
				Entry("with valid timeout within range value 10", "600", 600),
				Entry("with maximum allowed timeout value", "1200", 1200),
			)
		})
	})

	// This section tests IP obfuscation for security purposes in logs and error messages.
	Describe("The obfuscateIP function", func() {

		When("obfuscating IP addresses", func() {
			It("it should replace first three octets with asterisks", func() {
				ip := "203.0.113.1"
				expectedObfuscation := "***.***.***.1"
				Expect(obfuscateIP(ip)).Should(Equal(expectedObfuscation))
			})
		})
	})

	// This spec only tests the IP format validation side of validateIP, since validateIP calls on
	// ec2_helpers.PingIPAddress and it's pinging capabilities are already test-covered in ec2_helpers_test.go.
	Describe("The validateIP function", func() {

		When("validating invalid IP formats", func() {
			DescribeTable("it should return errInvalidIPFormat",
				func(ip string) {
					Expect(validateIP(ip)).Should(MatchError(errInvalidIPFormat))
				},
				Entry("with empty string", ""),
				Entry("with invalid characters", "abc.def.ghi.jkl"),
				Entry("with special characters", "abc.def.&.jkl"),
				Entry("with incomplete octets", "203.0.1"),
				Entry("with too many octets", "203.0.113.1.1"),
				Entry("with non-numeric octets", "203.KokoHazamar.113.1"),
				Entry("with octet exceeding 255", "203.0.113.256"),
				Entry("with negative octets", "203.0.-113.1"),
			)
		})
	})

	// This section tests validation of IBM host secret configurations for s390x and ppc64le platforms.
	Describe("The validateIBMHostSecret function", func() {

		When("validating valid IBM host secrets", func() {
			DescribeTable("it should accept matching platform substrings",
				func(key, value string) {
					Expect(validateIBMHostSecret(key, value)).ShouldNot(HaveOccurred())
				},
				Entry("with s390x in both key and value", "host-s390x-prod", "config-s390x-data"),
				Entry("with ppc64le in both key and value", "host-ppc64le-dev", "setup-ppc64le-config"),
				Entry("with IBM platform substring anywhere", "prod-s390x", "s390x-host-001"),
				Entry("with IBM platform substring anywhere", "ppc64le-config", "host-ppc64le"),
				Entry("with just IBM platform", "ppc64le", "ppc64le"),
			)
		})

		When("validating invalid IBM host secrets", func() {
			It("should return error when value is empty", func() {
				err := validateIBMHostSecret("host-s390x", "")
				Expect(err).Should(MatchError(errIBMHostSecretEmpty))
			})

			It("should return error when value is only whitespace", func() {
				err := validateIBMHostSecret("host-s390x", "   ")
				Expect(err).Should(MatchError(errIBMHostSecretEmpty))
			})

			DescribeTable("it should return error when platform substrings don't match",
				func(key, value string) {
					err := validateIBMHostSecret(key, value)
					Expect(err).Should(MatchError(errIBMHostSecretPlatformMismatch))
				},
				Entry("with s390x in key but ppc64le in value", "host-s390x", "config-ppc64le"),
				Entry("with ppc64le in key but s390x in value", "host-ppc64le", "config-s390x"),
				Entry("with no platform in key", "host-prod", "config-s390x"),
				Entry("with no platform in value", "host-s390x", "config-prod"),
				Entry("with no platform in either", "host-prod", "config-dev"),
			)
		})
	})

	// This section tests parsing of dynamic host instance type configuration keys for AWS EC2.
	Describe("The parseDynamicHostInstanceTypeKey function", func() {

		When("parsing valid platform config names", func() {
			DescribeTable("it should extract platform and instance type correctly",
				func(configName, expectedPlatform, expectedInstanceType string) {
					platform, instanceType, _ := parseDynamicHostInstanceTypeKey(configName)
					Expect(platform).Should(Equal(expectedPlatform))
					Expect(instanceType).Should(Equal(expectedInstanceType))
				},
				Entry("with simple format", "linux-m2xlarge-arm64", "arm64", "m2xlarge"),
				Entry("with multi-part instance type", "linux-d160-m4xlarge-amd64", "amd64", "d160-m4xlarge"),
				Entry("with multi-part instance type that requires normalization", "linux-m4xlarge-d160-arm64", "arm64", "d160-m4xlarge"),
			)
		})

		When("parsing invalid config key strings", func() {
			DescribeTable("it should return an error",
				func(value string) {
					_, _, err := parseDynamicHostInstanceTypValue(value)
					Expect(err).Should(HaveOccurred())
				},
				Entry("with too few parts", "m2xlargeamd64"),
				Entry("with empty string", ""),
			)
		})
	})

	// This section tests parsing of dynamic host instance type configuration values for AWS EC2.
	Describe("The parseDynamicHostInstanceTypValue function", func() {

		When("parsing valid config value strings", func() {
			DescribeTable("it should extract platform and instance type correctly",
				func(value, expectedPlatform, expectedInstanceType string) {
					platform, instanceType, _ := parseDynamicHostInstanceTypValue(value)
					Expect(platform).Should(Equal(expectedPlatform))
					Expect(instanceType).Should(Equal(expectedInstanceType))
				},
				Entry("with simple format", "prod-arm64-m2xlarge", "arm64", "m2xlarge"),
				Entry("with multi-part instance type", "prod-amd64-d160-m4xlarge", "amd64", "d160-m4xlarge"),
				Entry("with multi-part instance type that requires normalization", "prod-amd64-m4xlarge-d160", "amd64", "d160-m4xlarge"),
			)
		})

		When("parsing invalid config value strings", func() {
			DescribeTable("it should return an error",
				func(value string) {
					_, _, err := parseDynamicHostInstanceTypValue(value)
					Expect(err).Should(HaveOccurred())
				},
				Entry("with too few parts", "prodarm64"),
				Entry("with empty string", ""),
			)
		})
	})

	// This section tests end-to-end validation of dynamic instance tag configurations for AWS EC2.
	Describe("The validateDynamicInstanceTag function", func() {

		When("validating matching platform and instance type configurations", func() {
			DescribeTable("it should accept valid key-value pairs",
				func(key, value string) {
					Expect(validateDynamicInstanceTag(key, value)).ShouldNot(HaveOccurred())
				},
				Entry("with matching arm64 platform and instance type", "linux-m2xlarge-arm64", "prod-arm64-m2xlarge"),
				Entry("with multi-part instance type in correct order", "linux-d160-m4xlarge-arm64", "prod-arm64-d160-m4xlarge"),
				Entry("with multi-part instance type in different order", "linux-m4xlarge-d160-arm64", "prod-arm64-d160-m4xlarge"),
			)
		})

		When("validating mismatched configurations", func() {
			It("should return error when platforms don't match", func() {
				Expect(validateDynamicInstanceTag("linux-m2xlarge-arm64", "prod-amd64-m2xlarge").Error()).Should(ContainSubstring("platform mismatch"))
			})

			It("should return error when instance types don't match", func() {
				Expect(validateDynamicInstanceTag("linux-m2xlarge-arm64", "prod-arm64-t3large").Error()).Should(ContainSubstring("instance type mismatch"))
			})
		})

		When("validating invalid key or value formats", func() {
			DescribeTable("it should return error when key or value formats are invalid",
				func(key, value string) {
					Expect(Expect(validateDynamicInstanceTag(key, value)).Should(HaveOccurred()))
				},
				Entry("with matching arm64 platform and instance type", "invalid-format", "prod-arm64-m2xlarge"),
				Entry("with multi-part instance type in correct order", "linux-m2xlarge-arm64", "invalid-format"),
			)
		})
	})
})
