// This file contains tests for the host configuration parsing and validation functions.
// It covers parsing of local platforms, dynamic platforms, dynamic pool platforms, and static hosts.
package taskrun

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//TODO: add 'const systemNamespace = "multi-platform-controller"' here at the start of KFLUXINFRA-2328

var _ = Describe("Host Configuration Parsing and Validation Tests", func() {

	// This section tests the parsePlatformList helper function
	Describe("The parsePlatformList helper function", func() {

		When("parsing valid platform lists", func() {
			DescribeTable("should parse platform lists correctly",
				func(ctx SpecContext, input string, expected []string) {
					result, _ := parsePlatformList(input, PlatformTypeDynamic)
					Expect(result).Should(Equal(expected))
				},
				Entry("empty string returns empty slice", "", []string{}),
				Entry("single platform", "linux/amd64", []string{"linux/amd64"}),
				Entry("multiple platforms", "linux/amd64,linux/arm64", []string{"linux/amd64", "linux/arm64"}),
				Entry("with whitespace", "linux/amd64 , linux/arm64 , linux/s390x", []string{"linux/amd64", "linux/arm64", "linux/s390x"}),
				Entry("with trailing comma", "linux/amd64,linux/arm64,", []string{"linux/amd64", "linux/arm64"}),
			)
		})

		When("parsing invalid platform lists", func() {
			DescribeTable("should return error for various invalid inputs",
				func(ctx SpecContext, input string, expectedErrorSubstring string) {
					_, err := parsePlatformList(input, PlatformTypeDynamic)
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("invalid platform format", "invalid_platform", "invalid dynamic platform 'invalid_platform'"),
				Entry("empty platform in middle of list", "linux/amd64,,linux/arm64", "empty platform in middle of list"),
				Entry("one platform in list is invalid", "linux/amd64,bad_format,linux/arm64", "invalid dynamic platform 'bad_format'"),
			)
		})
	})

	// This section tests the parseDynamicRequiredTypeField helper function
	Describe("The parseDynamicRequiredTypeField helper function", func() {

		When("extracting valid type field", func() {
			DescribeTable("should extract type field successfully",
				func(ctx SpecContext, typeName string) {
					data := map[string]string{
						"dynamic.linux-amd64.type": typeName,
					}
					Expect(parseDynamicRequiredTypeField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform")).Should(Equal(typeName))
				},
				Entry("for AWS type", "aws"),
				Entry("for IBM type", "ibm"),
				Entry("for any arbitrary type", "gcp"),
			)
		})

		When("type field is missing", func() {
			It("should return error", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.max-instances": "10",
				}
				_, err := parseDynamicRequiredTypeField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform")
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("type field is required"))
			})
		})

		When("type field is empty string", func() {
			It("should return error", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.type": "",
				}
				_, err := parseDynamicRequiredTypeField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform")
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("type field is required"))
			})
		})
	})

	// This section tests the parseDynamicRequiredMaxInstancesField helper function
	Describe("The parseDynamicRequiredMaxInstancesField helper function", func() {

		When("extracting valid max-instances field", func() {
			DescribeTable("should extract and validate max-instances successfully",
				func(ctx SpecContext, value string, expected int) {
					data := map[string]string{
						"dynamic.linux-amd64.max-instances": value,
					}
					Expect(parseDynamicRequiredMaxInstancesField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform")).Should(Equal(expected))
				},
				Entry("for minimum value", "1", 1),
				Entry("for maximum value", "250", 250),
			)
		})

		When("max-instances field is missing", func() {
			It("should return error", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.type": "aws",
				}
				_, err := parseDynamicRequiredMaxInstancesField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform")
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("max-instances field is required"))
			})
		})

		When("max-instances field is invalid", func() {
			DescribeTable("should return validation error",
				func(ctx SpecContext, value string, expectedErrorSubstring string) {
					data := map[string]string{
						"dynamic.linux-amd64.max-instances": value,
					}
					_, err := parseDynamicRequiredMaxInstancesField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform")
					Expect(err).Should(HaveOccurred())
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("for non-numeric value", "abc", "invalid max-instances 'abc'"),
				Entry("for zero value", "0", "invalid max-instances '0'"),
				Entry("for negative value", "-5", "invalid max-instances '-5'"),
			)
		})
	})

	// This section tests the parseDynamicOptionalInstanceTagField helper function
	Describe("The parseDynamicOptionalInstanceTagField helper function", func() {

		When("extracting valid instance-tag field", func() {
			It("should extract and validate instance-tag successfully", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-m2xlarge-amd64.instance-tag": "prod-amd64-m2xlarge",
				}
				Expect(parseDynamicOptionalInstanceTagField(data, "dynamic.linux-m2xlarge-amd64.", "linux-m2xlarge-amd64", "linux-m2xlarge/amd64", "dynamic platform")).Should(Equal("prod-amd64-m2xlarge"))
			})
		})

		When("instance-tag field is missing", func() {
			It("should return empty string without error", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.type": "aws",
				}
				Expect(parseDynamicOptionalInstanceTagField(data, "dynamic.linux-amd64.", "linux-amd64", "linux/amd64", "dynamic platform")).Should(BeEmpty())
			})
		})

		When("instance-tag field is invalid", func() {
			It("should return validation error", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-m2xlarge-amd64.instance-tag": "invalid-tag",
				}
				_, err := parseDynamicOptionalInstanceTagField(data, "dynamic.linux-m2xlarge-amd64.", "linux-m2xlarge-amd64", "linux-m2xlarge/amd64", "dynamic platform")
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("invalid instance-tag 'invalid-tag'"))
			})
		})
	})

	// This section tests the parseRequiredSSHSecretField helper function
	Describe("The parseRequiredSSHSecretField helper function", func() {

		When("extracting valid ssh-secret for AWS", func() {
			It("should extract ssh-secret successfully", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.ssh-secret": "aws-secret-name",
				}
				Expect(parseRequiredSSHSecretField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform", "aws")).Should(Equal("aws-secret-name"))
			})
		})

		When("ssh-secret field is missing", func() {
			It("should return error", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.type": "aws",
				}
				_, err := parseRequiredSSHSecretField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform", "aws")
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("ssh-secret field is required"))
			})
		})

		When("extracting valid ssh-secret for IBM", func() {
			DescribeTable("should extract and validate ssh-secret successfully",
				func(ctx SpecContext, platform, secret string) {
					prefix := "dynamic." + strings.ReplaceAll(platform, "/", "-") + "."
					data := map[string]string{
						prefix + "ssh-secret": secret,
					}
					Expect(parseRequiredSSHSecretField(data, prefix, platform, "dynamic platform", "ibm")).Should(Equal(secret))
				},
				Entry("for s390x platform", "linux/s390x", "ibm-s390x-secret"),
				Entry("for ppc64le platform", "linux/ppc64le", "ibm-ppc64le-secret"),
			)
		})

		When("ssh-secret validation fails for IBM platform", func() {
			It("should return validation error", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-s390x.ssh-secret": "invalid-secret-no-platform",
				}
				_, err := parseRequiredSSHSecretField(data, "dynamic.linux-s390x.", "linux/s390x", "dynamic platform", "ibm")
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("invalid ssh-secret"))
			})
		})

		When("ssh-secret field is empty or whitespace", func() {
			DescribeTable("should return error",
				func(ctx SpecContext, value string) {
					data := map[string]string{
						"dynamic.linux-amd64.ssh-secret": value,
					}
					_, err := parseRequiredSSHSecretField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform", "aws")
					Expect(err).Should(HaveOccurred())
					Expect(err.Error()).Should(ContainSubstring("ssh-secret field is required"))
				},
				Entry("for empty string", ""),
				Entry("for whitespace only", "   "),
				Entry("for tabs", "\t\t"),
			)
		})

		When("cloud provider type is invalid", func() {
			It("should return error", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.type":       "KokoHazamar",
					"dynamic.linux-amd64.ssh-secret": "some-secret",
				}
				_, err := parseRequiredSSHSecretField(data, "dynamic.linux-amd64.", "linux/amd64", "dynamic platform", "KokoHazamar")
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("invalid type: expect 'ibm' or 'aws'"))
			})
		})
	})

	// This section tests the parseDynamicPlatformConfig function
	Describe("The parseDynamicPlatformConfig function", func() {

		When("parsing valid AWS dynamic platform configurations", func() {
			It("should parse complete AWS dynamic platform with all optional fields", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.type":               "aws",
					"dynamic.linux-amd64.max-instances":      "10",
					"dynamic.linux-amd64.allocation-timeout": "900",
					"dynamic.linux-amd64.ssh-secret":         "aws-secret",
					"dynamic.linux-amd64.sudo-commands":      "yum install -y docker",
				}

				dynamicConfig, err := parseDynamicPlatformConfig(data, "linux/amd64")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(dynamicConfig.Type).Should(Equal("aws"))
				Expect(dynamicConfig.MaxInstances).Should(Equal(10))
				Expect(dynamicConfig.AllocationTimeout).Should(Equal(int64(900)))
				Expect(dynamicConfig.SSHSecret).Should(Equal("aws-secret"))
				Expect(dynamicConfig.InstanceTag).Should(BeEmpty()) // optional field
				Expect(dynamicConfig.SudoCommands).Should(Equal("yum install -y docker"))
			})

			It("should parse IBM platform with default timeout", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-s390x.type":          "ibm",
					"dynamic.linux-s390x.max-instances": "3",
					"dynamic.linux-s390x.ssh-secret":    "ibm-s390x-secret",
				}

				ibmzConfig, err := parseDynamicPlatformConfig(data, "linux/s390x")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(ibmzConfig.Type).Should(Equal("ibm"))
				Expect(ibmzConfig.MaxInstances).Should(Equal(3))
				Expect(ibmzConfig.AllocationTimeout).Should(Equal(int64(defaultAllocationTimeout)))
				Expect(ibmzConfig.SSHSecret).Should(Equal("ibm-s390x-secret"))
				Expect(ibmzConfig.InstanceTag).Should(BeEmpty())
				Expect(ibmzConfig.SudoCommands).Should(BeEmpty())
			})
		})

		When("parsing invalid dynamic platform configurations", func() {
			DescribeTable("should return error and empty config for various invalid configurations",
				func(ctx SpecContext, data map[string]string, platform string, expectedErrorSubstring string) {
					dynamicConfig, err := parseDynamicPlatformConfig(data, platform)
					Expect(dynamicConfig).Should(Equal(DynamicPlatformConfig{}))
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("when type field is missing",
					map[string]string{
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"type field is required",
				),
				Entry("when max-instances field is missing",
					map[string]string{
						"dynamic.linux-amd64.type":       "aws",
						"dynamic.linux-amd64.ssh-secret": "aws-secret",
					},
					"linux/amd64",
					"max-instances field is required",
				),
				Entry("when ssh-secret field is missing",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
					},
					"linux/amd64",
					"ssh-secret field is required",
				),
				Entry("for invalid max-instances value",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "invalid",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"invalid max-instances 'invalid'",
				),
				Entry("for invalid instance-tag value",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.instance-tag":  "invalid-tag-format",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"invalid instance-tag 'invalid-tag-format'",
				),
				Entry("for AWS platform with empty ssh-secret",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.ssh-secret":    "   ",
					},
					"linux/amd64",
					"ssh-secret field is required",
				),
				Entry("for IBM platform with invalid ssh-secret",
					map[string]string{
						"dynamic.linux-s390x.type":          "ibm",
						"dynamic.linux-s390x.max-instances": "3",
						"dynamic.linux-s390x.ssh-secret":    "invalid-secret",
					},
					"linux/s390x",
					"invalid ssh-secret 'invalid-secret'",
				),
			)
		})
	})

	// This section tests the parseDynamicPoolPlatformConfig function
	// Note: Tests focus on pool-specific fields (concurrency, max-age) since shared fields
	// (type, max-instances, ssh-secret, instance-tag) are already tested in parseDynamicPlatformConfig
	Describe("The parseDynamicPoolPlatformConfig function", func() {

		When("parsing valid pool-specific fields", func() {
			It("should parse pool platform with valid concurrency and max-age", func(ctx SpecContext) {
				data := map[string]string{
					"dynamic.linux-amd64.type":          "aws",
					"dynamic.linux-amd64.max-instances": "20",
					"dynamic.linux-amd64.concurrency":   "4",
					"dynamic.linux-amd64.max-age":       "60",
					"dynamic.linux-amd64.ssh-secret":    "aws-pool-secret",
				}

				poolConfig, err := parseDynamicPoolPlatformConfig(data, "linux/amd64")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(poolConfig.Concurrency).Should(Equal(4))
				Expect(poolConfig.MaxAge).Should(Equal(60))
			})

			DescribeTable("should parse valid concurrency values",
				func(ctx SpecContext, value string, expected int) {
					data := map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   value,
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					}
					poolConfig, err := parseDynamicPoolPlatformConfig(data, "linux/amd64")
					Expect(err).ShouldNot(HaveOccurred())
					Expect(poolConfig.Concurrency).Should(Equal(expected))
				},
				Entry("for minimum concurrency", "1", 1),
				Entry("for maximum concurrency", "8", 8),
			)

			DescribeTable("should parse valid max-age values",
				func(ctx SpecContext, value string, expected int) {
					data := map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.max-age":       value,
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					}
					poolConfig, err := parseDynamicPoolPlatformConfig(data, "linux/amd64")
					Expect(err).ShouldNot(HaveOccurred())
					Expect(poolConfig.MaxAge).Should(Equal(expected))
				},
				Entry("for minimum max-age", "1", 1),
				Entry("for maximum max-age (24 hours)", "1440", 1440),
			)
		})

		When("parsing invalid pool-specific fields", func() {
			DescribeTable("should return error and empty config for invalid pool-specific configurations",
				func(ctx SpecContext, data map[string]string, platform string, expectedErrorSubstring string) {
					poolConfig, err := parseDynamicPoolPlatformConfig(data, platform)
					Expect(poolConfig).Should(Equal(DynamicPoolPlatformConfig{}))
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("when concurrency field is missing",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"concurrency field is required",
				),
				Entry("when max-age field is missing",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"max-age field is required",
				),
				Entry("for invalid concurrency value",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "invalid",
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"invalid concurrency 'invalid'",
				),
				Entry("for concurrency exceeding limit",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "99",
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"invalid concurrency '99'",
				),
				Entry("for concurrency zero",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "0",
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"invalid concurrency '0'",
				),
				Entry("for invalid max-age value",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.max-age":       "invalid",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"invalid max-age 'invalid'",
				),
				Entry("for max-age exceeding limit",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.max-age":       "9999",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"invalid max-age '9999'",
				),
				Entry("for max-age zero",
					map[string]string{
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.max-age":       "0",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"linux/amd64",
					"invalid max-age '0'",
				),
			)
		})
	})

	// This section tests the parseStaticHostConfig function
	Describe("The parseStaticHostConfig function", func() {

		When("parsing valid static host configurations", func() {
			DescribeTable("should parse static host configurations correctly",
				func(fieldsToOverride map[string]string, expectedAddress, expectedUser, expectedPlatform, expectedSecret string, expectedConcurrency int) {
					// Base data with all fields valid
					data := map[string]string{
						"host.moshe-kipod-s390x-static.address":     "127.0.0.1",
						"host.moshe-kipod-s390x-static.user":        "root",
						"host.moshe-kipod-s390x-static.platform":    "linux/s390x",
						"host.moshe-kipod-s390x-static.secret":      "test-s390x-static-secret",
						"host.moshe-kipod-s390x-static.concurrency": "4",
					}
					// Override fields for specific test cases
					for field, value := range fieldsToOverride {
						data["host.moshe-kipod-s390x-static."+field] = value
					}
					hostConfig, err := parseStaticHostConfig(data, "moshe-kipod-s390x-static")
					Expect(err).ShouldNot(HaveOccurred())
					Expect(hostConfig.Address).Should(Equal(expectedAddress))
					Expect(hostConfig.User).Should(Equal(expectedUser))
					Expect(hostConfig.Platform).Should(Equal(expectedPlatform))
					Expect(hostConfig.Secret).Should(Equal(expectedSecret))
					Expect(hostConfig.Concurrency).Should(Equal(expectedConcurrency))
				},
				Entry("with all fields",
					map[string]string{},
					"127.0.0.1", "root", "linux/s390x", "test-s390x-static-secret", 4,
				),
				Entry("with user field containing whitespace that gets trimmed",
					map[string]string{"user": "  root  "},
					"127.0.0.1", "root", "linux/s390x", "test-s390x-static-secret", 4,
				),
				Entry("with no concurrency field defaults to 0",
					map[string]string{"concurrency": ""},
					"127.0.0.1", "root", "linux/s390x", "test-s390x-static-secret", 0,
				),
			)
		})

		When("parsing invalid static host configurations", func() {
			DescribeTable("should return error and empty config for various invalid configurations",
				func(fieldsToOverride map[string]string, expectedErrorSubstring string) {
					// Base data with all fields valid
					data := map[string]string{
						"host.koko-hazamar-s390x-static.address":     "127.0.0.1",
						"host.koko-hazamar-s390x-static.user":        "root",
						"host.koko-hazamar-s390x-static.platform":    "linux/s390x",
						"host.koko-hazamar-s390x-static.secret":      "test-s390x-static-secret",
						"host.koko-hazamar-s390x-static.concurrency": "4",
					}
					// Override fields for specific test cases
					for field, value := range fieldsToOverride {
						data["host.koko-hazamar-s390x-static."+field] = value
					}
					hostConfig, err := parseStaticHostConfig(data, "koko-hazamar-s390x-static")
					Expect(hostConfig).Should(Equal(StaticHostConfig{}))
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("when address field is missing",
					map[string]string{"address": ""},
					"address field is required",
				),
				Entry("for malformed IP address",
					map[string]string{"address": "not-an-ip"},
					"invalid address 'not-an-ip'",
				),
				Entry("for invalid platform format",
					map[string]string{"platform": "invalid_platform"},
					"invalid platform 'invalid_platform'",
				),
				Entry("for invalid concurrency value",
					map[string]string{"concurrency": "invalid"},
					"invalid concurrency 'invalid'",
				),
				Entry("for concurrency exceeding limit",
					map[string]string{"concurrency": "99"},
					"invalid concurrency '99'",
				),
				Entry("for IBM platform with invalid secret",
					map[string]string{"secret": "invalid-secret"},
					"invalid secret 'invalid-secret'",
				),
			)
		})
	})
})
