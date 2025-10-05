// This file contains tests for the host configuration parsing and validation functions.
// It covers parsing of local platforms, dynamic platforms, dynamic pool platforms, static hosts,
// and instance tagging configuration from ConfigMap data into structured ClusterHostConfig objects.
package taskrun

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

//TODO: add 'const systemNamespace = "multi-platform-controller"' here at the start of KFLUXINFRA-2328

var _ = Describe("Host Configuration Parsing and Validation Tests", func() {

	// This section tests the parsePlatformList helper function
	Describe("The parsePlatformList helper function", func() {

		When("parsing valid platform lists", func() {
			DescribeTable("should parse platform lists correctly",
				func(ctx SpecContext, input string, platformType string, expected []string) {
					result, _ := parsePlatformList(input, platformType)
					Expect(result).Should(Equal(expected))
				},
				Entry("empty string returns empty slice", "", "test",
					[]string{}),
				Entry("single platform", "linux/amd64", "test",
					[]string{"linux/amd64"}),
				Entry("multiple platforms", "linux/amd64,linux/arm64", "test",
					[]string{"linux/amd64", "linux/arm64"}),
				Entry("with whitespace", "linux/amd64 , linux/arm64 , linux/s390x", "test",
					[]string{"linux/amd64", "linux/arm64", "linux/s390x"}),
				Entry("with trailing comma", "linux/amd64,linux/arm64,", "test",
					[]string{"linux/amd64", "linux/arm64"}),
			)
		})

		When("parsing invalid platform lists", func() {
			DescribeTable("should return error for various invalid inputs",
				func(ctx SpecContext, input string, platformType string, expectedErrorSubstring string) {
					_, err := parsePlatformList(input, platformType)
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("invalid platform format",
					"invalid_platform", "test", "invalid test platform 'invalid_platform'"),
				Entry("empty platform in middle of list",
					"linux/amd64,,linux/arm64", "test", "empty platform in middle of list"),
				Entry("one platform in list is invalid",
					"linux/amd64,bad_format,linux/arm64", "test", "invalid test platform 'bad_format'"),
			)
		})
	})

	// This section tests parsing and validation of local platform configurations
	Describe("The ParseAndValidate function with local platforms", func() {

		When("parsing valid local platform configurations", func() {
			DescribeTable("should parse local platform configurations correctly",
				func(ctx SpecContext, localPlatforms string, expected []string) {
					data := map[string]string{
						LocalPlatforms: localPlatforms,
					}
					config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
					Expect(config.LocalPlatforms).Should(Equal(expected))
				},
				Entry("single local platform", "linux/amd64",
					[]string{"linux/amd64"}),
				Entry("multiple local platforms", "linux/amd64,linux/s390x",
					[]string{"linux/amd64", "linux/s390x"}),
				Entry("local platforms with whitespace", "linux/amd64, local , localhost",
					[]string{"linux/amd64", "local", "localhost"}),
				Entry("special exception platforms", "local,localhost,linux/x86_64",
					[]string{"local", "localhost", "linux/x86_64"}),
			)

			DescribeTable("should return empty list for various empty configurations",
				func(ctx SpecContext, data map[string]string) {
					config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
					Expect(config.LocalPlatforms).Should(BeEmpty())
				},
				Entry("when local platforms is empty string", map[string]string{
					LocalPlatforms: "",
				}),
				Entry("when local platforms is not present", map[string]string{}),
			)
		})

		When("parsing invalid local platform configurations", func() {
			DescribeTable("should return error for invalid platform formats",
				func(ctx SpecContext, localPlatforms string, expectedErrorSubstring string) {
					data := map[string]string{
						LocalPlatforms: localPlatforms,
					}
					_, err := ParseAndValidate(ctx, nil, data, systemNamespace)
					Expect(err).Should(HaveOccurred())
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("for invalid platform format",
					"invalid_platform", "invalid local platform 'invalid_platform'"),
				Entry("when one platform in list is invalid",
					"linux/arm64,bad/format/too/many,linux/s390x", "invalid local platform 'bad/format/too/many'"),
			)
		})
	})

	// This section tests parsing and validation of dynamic platform configurations
	Describe("The ParseAndValidate function with dynamic platforms", func() {

		When("parsing valid AWS dynamic platform configurations", func() {
			It("should parse complete AWS dynamic platform with all optional fields", func(ctx SpecContext) {
				data := map[string]string{
					DynamicPlatforms:                         "linux/amd64",
					"dynamic.linux-amd64.type":               "aws",
					"dynamic.linux-amd64.max-instances":      "10",
					"dynamic.linux-amd64.allocation-timeout": "900",
					"dynamic.linux-amd64.ssh-secret":         "aws-secret",
					"dynamic.linux-amd64.sudo-commands":      "yum install -y docker",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.DynamicPlatforms).Should(HaveKey("linux/amd64"))

				dynamicConfig := config.DynamicPlatforms["linux/amd64"]
				Expect(dynamicConfig.Type).Should(Equal("aws"))
				Expect(dynamicConfig.MaxInstances).Should(Equal(10))
				Expect(dynamicConfig.AllocationTimeout).Should(Equal(int64(900)))
				Expect(dynamicConfig.SSHSecret).Should(Equal("aws-secret"))
				Expect(dynamicConfig.InstanceTag).Should(BeEmpty()) // optional field
				Expect(dynamicConfig.SudoCommands).Should(Equal("yum install -y docker"))
			})

			It("should parse multiple dynamic platforms of different cloud types successfully", func(ctx SpecContext) {
				data := map[string]string{
					DynamicPlatforms:                      "linux/amd64,linux/s390x,linux/ppc64le",
					"dynamic.linux-amd64.type":            "aws",
					"dynamic.linux-amd64.max-instances":   "5",
					"dynamic.linux-amd64.ssh-secret":      "aws-arm-secret",
					"dynamic.linux-s390x.type":            "ibmz",
					"dynamic.linux-s390x.max-instances":   "3",
					"dynamic.linux-s390x.ssh-secret":      "ibm-s390x-secret",
					"dynamic.linux-ppc64le.type":          "ibmp",
					"dynamic.linux-ppc64le.max-instances": "2",
					"dynamic.linux-ppc64le.ssh-secret":    "ibm-ppc64le-secret",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.DynamicPlatforms).Should(HaveLen(3))

				// Verify AWS platform
				awsConfig := config.DynamicPlatforms["linux/amd64"]
				Expect(awsConfig.Type).Should(Equal("aws"))
				Expect(awsConfig.MaxInstances).Should(Equal(5))
				Expect(awsConfig.InstanceTag).Should(BeEmpty())
				Expect(awsConfig.AllocationTimeout).Should(Equal(int64(defaultAllocationTimeout)))
				Expect(awsConfig.SSHSecret).Should(Equal("aws-arm-secret"))
				Expect(awsConfig.SudoCommands).Should(BeEmpty())

				// Verify IBM Z platform
				ibmzConfig := config.DynamicPlatforms["linux/s390x"]
				Expect(ibmzConfig.Type).Should(Equal("ibmz"))
				Expect(ibmzConfig.MaxInstances).Should(Equal(3))
				Expect(ibmzConfig.SSHSecret).Should(Equal("ibm-s390x-secret"))

				// Verify IBM Power platform
				ibmpConfig := config.DynamicPlatforms["linux/ppc64le"]
				Expect(ibmpConfig.Type).Should(Equal("ibmp"))
				Expect(ibmpConfig.MaxInstances).Should(Equal(2))
				Expect(ibmpConfig.SSHSecret).Should(Equal("ibm-ppc64le-secret"))
			})
		})

		When("parsing invalid dynamic platform configurations", func() {
			DescribeTable("should return error for various invalid configurations",
				func(ctx SpecContext, data map[string]string, expectedErrorSubstring string) {
					_, err := ParseAndValidate(ctx, nil, data, systemNamespace)
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("for invalid platform format",
					map[string]string{
						DynamicPlatforms: "invalid_platform",
					},
					"invalid dynamic platform 'invalid_platform'",
				),
				Entry("when type field is missing",
					map[string]string{
						DynamicPlatforms:                    "linux/amd64",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"type field is required",
				),
				Entry("when max-instances field is missing",
					map[string]string{
						DynamicPlatforms:                 "linux/amd64",
						"dynamic.linux-amd64.type":       "aws",
						"dynamic.linux-amd64.ssh-secret": "aws-secret",
					},
					"max-instances field is required",
				),
				Entry("when ssh-secret field is missing",
					map[string]string{
						DynamicPlatforms:                    "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
					},
					"ssh-secret field is required",
				),
				Entry("for invalid max-instances value",
					map[string]string{
						DynamicPlatforms:                    "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "invalid",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"invalid max-instances 'invalid'",
				),
				Entry("for max-instances exceeding limit",
					map[string]string{
						DynamicPlatforms:                    "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "999",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"invalid max-instances '999'",
				),
				Entry("for allocation-timeout exceeding limit",
					map[string]string{
						DynamicPlatforms:                         "linux/amd64",
						"dynamic.linux-amd64.type":               "aws",
						"dynamic.linux-amd64.max-instances":      "10",
						"dynamic.linux-amd64.allocation-timeout": "9999",
						"dynamic.linux-amd64.ssh-secret":         "aws-secret",
					},
					"invalid allocation-timeout '9999'",
				),
				Entry("for invalid instance-tag value",
					map[string]string{
						DynamicPlatforms:                    "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.instance-tag":  "invalid-tag-format",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"invalid instance-tag 'invalid-tag-format'",
				),
				Entry("for AWS platform with empty ssh-secret",
					map[string]string{
						DynamicPlatforms:                    "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.ssh-secret":    "   ",
					},
					"ssh-secret cannot be empty for AWS platforms",
				),
				Entry("for IBM platform with invalid ssh-secret",
					map[string]string{
						DynamicPlatforms:                    "linux/s390x",
						"dynamic.linux-s390x.type":          "ibmz",
						"dynamic.linux-s390x.max-instances": "3",
						"dynamic.linux-s390x.ssh-secret":    "invalid-secret",
					},
					"invalid ssh-secret 'invalid-secret'",
				),
			)
		})
	})

	// This section tests parsing and validation of dynamic pool platform configurations
	Describe("The ParseAndValidate function with dynamic pool platforms", func() {

		When("parsing valid dynamic pool platform configurations", func() {
			It("should parse complete dynamic pool platform with all optional fields", func(ctx SpecContext) {
				data := map[string]string{
					DynamicPoolPlatforms:                         "linux-m2xlarge/amd64",
					"dynamic.linux-m2xlarge-amd64.type":          "aws",
					"dynamic.linux-m2xlarge-amd64.max-instances": "20",
					"dynamic.linux-m2xlarge-amd64.concurrency":   "4",
					"dynamic.linux-m2xlarge-amd64.max-age":       "60",
					"dynamic.linux-m2xlarge-amd64.instance-tag":  "prod-amd64-m2xlarge",
					"dynamic.linux-m2xlarge-amd64.ssh-secret":    "aws-pool-secret",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.DynamicPoolPlatforms).Should(HaveKey("linux-m2xlarge/amd64"))

				poolConfig := config.DynamicPoolPlatforms["linux-m2xlarge/amd64"]
				Expect(poolConfig.Type).Should(Equal("aws"))
				Expect(poolConfig.MaxInstances).Should(Equal(20))
				Expect(poolConfig.Concurrency).Should(Equal(4))
				Expect(poolConfig.MaxAge).Should(Equal(60))
				Expect(poolConfig.InstanceTag).Should(Equal("prod-amd64-m2xlarge"))
				Expect(poolConfig.SSHSecret).Should(Equal("aws-pool-secret"))
			})

			It("should parse multiple dynamic pool platforms of different cloud types successfully", func(ctx SpecContext) {
				data := map[string]string{
					DynamicPoolPlatforms:                "linux/amd64,linux/arm64,linux/s390x",
					"dynamic.linux-amd64.type":          "aws",
					"dynamic.linux-amd64.max-instances": "20",
					"dynamic.linux-amd64.concurrency":   "4",
					"dynamic.linux-amd64.max-age":       "60",
					"dynamic.linux-amd64.ssh-secret":    "aws-amd64-pool-secret",
					"dynamic.linux-arm64.type":          "aws",
					"dynamic.linux-arm64.max-instances": "15",
					"dynamic.linux-arm64.concurrency":   "3",
					"dynamic.linux-arm64.max-age":       "120",
					"dynamic.linux-arm64.ssh-secret":    "aws-arm64-pool-secret",
					"dynamic.linux-s390x.type":          "ibmz",
					"dynamic.linux-s390x.max-instances": "5",
					"dynamic.linux-s390x.concurrency":   "2",
					"dynamic.linux-s390x.max-age":       "180",
					"dynamic.linux-s390x.ssh-secret":    "ibm-s390x-pool-secret",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.DynamicPoolPlatforms).Should(HaveLen(3))

				// Verify AWS amd64 platform
				amd64Config := config.DynamicPoolPlatforms["linux/amd64"]
				Expect(amd64Config.Type).Should(Equal("aws"))
				Expect(amd64Config.MaxInstances).Should(Equal(20))
				Expect(amd64Config.Concurrency).Should(Equal(4))
				Expect(amd64Config.MaxAge).Should(Equal(60))
				Expect(amd64Config.SSHSecret).Should(Equal("aws-amd64-pool-secret"))

				// Verify AWS arm64 platform
				arm64Config := config.DynamicPoolPlatforms["linux/arm64"]
				Expect(arm64Config.Type).Should(Equal("aws"))
				Expect(arm64Config.MaxInstances).Should(Equal(15))
				Expect(arm64Config.Concurrency).Should(Equal(3))
				Expect(arm64Config.MaxAge).Should(Equal(120))
				Expect(arm64Config.SSHSecret).Should(Equal("aws-arm64-pool-secret"))

				// Verify IBM Z platform
				ibmzConfig := config.DynamicPoolPlatforms["linux/s390x"]
				Expect(ibmzConfig.Type).Should(Equal("ibmz"))
				Expect(ibmzConfig.MaxInstances).Should(Equal(5))
				Expect(ibmzConfig.Concurrency).Should(Equal(2))
				Expect(ibmzConfig.MaxAge).Should(Equal(180))
				Expect(ibmzConfig.SSHSecret).Should(Equal("ibm-s390x-pool-secret"))
			})
		})

		When("parsing invalid dynamic pool platform configurations", func() {
			DescribeTable("should return error for various invalid configurations",
				func(ctx SpecContext, data map[string]string, expectedErrorSubstring string) {
					_, err := ParseAndValidate(ctx, nil, data, systemNamespace)
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("for invalid platform format",
					map[string]string{
						DynamicPoolPlatforms: "invalid_platform",
						//"dynamic.linux-amd64.type": "aws",
					},
					"invalid dynamic pool platform 'invalid_platform'",
				),
				Entry("when type field is missing",
					map[string]string{
						DynamicPoolPlatforms:                "linux/amd64",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"type field is required",
				),
				Entry("when max-instances field is missing",
					map[string]string{
						DynamicPoolPlatforms:              "linux/amd64",
						"dynamic.linux-amd64.type":        "aws",
						"dynamic.linux-amd64.concurrency": "4",
						"dynamic.linux-amd64.max-age":     "60",
						"dynamic.linux-amd64.ssh-secret":  "aws-secret",
					},
					"max-instances field is required",
				),
				Entry("when concurrency field is missing",
					map[string]string{
						DynamicPoolPlatforms:                "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"concurrency field is required",
				),
				Entry("when max-age field is missing",
					map[string]string{
						DynamicPoolPlatforms:                "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"max-age field is required",
				),
				Entry("when ssh-secret field is missing",
					map[string]string{
						DynamicPoolPlatforms:                "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.max-age":       "60",
					},
					"ssh-secret field is required",
				),
				Entry("for invalid concurrency value",
					map[string]string{
						DynamicPoolPlatforms:                "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "invalid",
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"invalid concurrency 'invalid'",
				),
				Entry("for concurrency exceeding limit",
					map[string]string{
						DynamicPoolPlatforms:                "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "99",
						"dynamic.linux-amd64.max-age":       "60",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"invalid concurrency '99'",
				),
				Entry("for max-age exceeding limit",
					map[string]string{
						DynamicPoolPlatforms:                "linux/amd64",
						"dynamic.linux-amd64.type":          "aws",
						"dynamic.linux-amd64.max-instances": "10",
						"dynamic.linux-amd64.concurrency":   "4",
						"dynamic.linux-amd64.max-age":       "9999",
						"dynamic.linux-amd64.ssh-secret":    "aws-secret",
					},
					"invalid max-age '9999'",
				),
				Entry("for invalid instance-tag value",
					map[string]string{
						DynamicPoolPlatforms:                         "linux-m2xlarge/amd64",
						"dynamic.linux-m2xlarge-amd64.type":          "aws",
						"dynamic.linux-m2xlarge-amd64.max-instances": "10",
						"dynamic.linux-m2xlarge-amd64.concurrency":   "4",
						"dynamic.linux-m2xlarge-amd64.max-age":       "60",
						"dynamic.linux-m2xlarge-amd64.instance-tag":  "invalid-tag",
						"dynamic.linux-m2xlarge-amd64.ssh-secret":    "aws-secret",
					},
					"invalid instance-tag 'invalid-tag'",
				),
				Entry("for IBM platform with invalid ssh-secret",
					map[string]string{
						DynamicPoolPlatforms:                "linux/s390x",
						"dynamic.linux-s390x.type":          "ibmz",
						"dynamic.linux-s390x.max-instances": "5",
						"dynamic.linux-s390x.concurrency":   "2",
						"dynamic.linux-s390x.max-age":       "180",
						"dynamic.linux-s390x.ssh-secret":    "invalid-secret",
					},
					"invalid ssh-secret 'invalid-secret'",
				),
			)
		})
	})

	// This section tests parsing and validation of static host configurations
	Describe("The ParseAndValidate function with static platforms", func() {

		When("parsing valid static host configurations", func() {
			It("should parse a single static host with all required fields successfully", func(ctx SpecContext) {
				data := map[string]string{
					"host.testhost1-s390x-static.address":     "127.0.0.1",
					"host.testhost1-s390x-static.user":        "root",
					"host.testhost1-s390x-static.platform":    "linux/s390x",
					"host.testhost1-s390x-static.secret":      "test-s390x-static-secret",
					"host.testhost1-s390x-static.concurrency": "4",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.StaticPlatforms).Should(HaveKey("testhost1-s390x-static"))
				Expect(config.StaticPlatforms["testhost1-s390x-static"].Platform).Should(Equal("linux/s390x"))
			})

			It("should parse multiple static hosts for the same platform successfully", func(ctx SpecContext) {
				data := map[string]string{
					"host.testhost1-s390x-static.address":     "127.0.0.1",
					"host.testhost1-s390x-static.user":        "koko_hazamar",
					"host.testhost1-s390x-static.platform":    "linux/s390x",
					"host.testhost1-s390x-static.secret":      "test-s390x-static-secret",
					"host.testhost1-s390x-static.concurrency": "4",
					"host.testhost2-s390x-static.address":     "127.0.0.1",
					"host.testhost2-s390x-static.user":        "moshe_kipod",
					"host.testhost2-s390x-static.platform":    "linux/s390x",
					"host.testhost2-s390x-static.secret":      "test-s390x-static-secret",
					"host.testhost2-s390x-static.concurrency": "2",
				}
				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.StaticPlatforms).Should(HaveKey("testhost1-s390x-static"))
				Expect(config.StaticPlatforms).Should(HaveKey("testhost2-s390x-static"))
				Expect(config.StaticPlatforms["testhost1-s390x-static"].Platform).Should(Equal("linux/s390x"))
				Expect(config.StaticPlatforms["testhost2-s390x-static"].Platform).Should(Equal("linux/s390x"))
			})

			It("should parse static hosts for different platforms successfully", func(ctx SpecContext) {
				data := map[string]string{
					"host.linux-arm64-c4largeboom.address":     "127.0.0.1",
					"host.linux-arm64-c4largeboom.user":        "root",
					"host.linux-arm64-c4largeboom.platform":    "linux/arm64",
					"host.linux-arm64-c4largeboom.secret":      "test-arm64-static-secret",
					"host.linux-arm64-c4largeboom.concurrency": "4",
					"host.testhost1-s390x-static.address":      "127.0.0.1",
					"host.testhost1-s390x-static.user":         "ubuntu",
					"host.testhost1-s390x-static.platform":     "linux/ppc64le",
					"host.testhost1-s390x-static.secret":       "test-ppc64le-secret",
					"host.testhost1-s390x-static.concurrency":  "2",
				}

				config, err := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(config.StaticPlatforms).Should(HaveLen(2))
				Expect(config.StaticPlatforms).Should(HaveKey("linux-arm64-c4largeboom"))
				Expect(config.StaticPlatforms).Should(HaveKey("testhost1-s390x-static"))
				Expect(config.StaticPlatforms["linux-arm64-c4largeboom"].Platform).Should(Equal("linux/arm64"))
				Expect(config.StaticPlatforms["testhost1-s390x-static"].Platform).Should(Equal("linux/ppc64le"))
			})
		})

		When("parsing invalid static host configurations", func() {
			It("should return error when address field is missing", func(ctx SpecContext) {
				data := map[string]string{
					"host.testhost1-s390x-static.user":        "root",
					"host.testhost1-s390x-static.platform":    "linux/s390x",
					"host.testhost1-s390x-static.secret":      "test-s390x-static-secret",
					"host.testhost1-s390x-static.concurrency": "4",
				}

				_, err := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(err.Error()).Should(ContainSubstring("address field is required"))
			})

			It("should return error for invalid IP address format", func(ctx SpecContext) {
				data := map[string]string{
					"host.testhost1-s390x-static.address":     "not-an-ip",
					"host.testhost1-s390x-static.user":        "root",
					"host.testhost1-s390x-static.platform":    "linux/s390x",
					"host.testhost1-s390x-static.secret":      "test-s390x-static-secret",
					"host.testhost1-s390x-static.concurrency": "4",
				}

				_, err := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(err.Error()).Should(ContainSubstring("invalid address 'not-an-ip'"))
			})

			DescribeTable("should ignore malformed key patterns",
				func(ctx SpecContext, data map[string]string, expectedHostCount int) {
					config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
					Expect(config.StaticPlatforms).Should(HaveLen(expectedHostCount))
				},
				Entry("when keys without host prefix are present", map[string]string{
					"host.validhost-s390x.address":     "127.0.0.1",
					"host.validhost-s390x.user":        "koko_hazamar",
					"other.config.value":               "something",
					"host.validhost-s390x.platform":    "linux/s390x",
					"host.validhost-s390x.secret":      "test-s390x-secret",
					"host.validhost-s390x.concurrency": "2",
				}, 1),
				Entry("when keys with no dots after host prefix are present", map[string]string{
					"host.validhost-s390x.address":     "127.0.0.1",
					"host.validhost-s390x.user":        "moshe_kipod",
					"host":                             "value",
					"host.validhost-s390x.platform":    "linux/s390x",
					"host.validhost-s390x.secret":      "test-s390x-secret",
					"host.validhost-s390x.concurrency": "2",
				}, 1),
				Entry("when keys with only one dot after host prefix are present", map[string]string{
					"host.validhost-s390x.address":     "127.0.0.1",
					"host.validhost-s390x.user":        "steve",
					"host.onlyhostname":                "value",
					"host.validhost-s390x.platform":    "linux/s390x",
					"host.validhost-s390x.secret":      "test-s390x-secret",
					"host.validhost-s390x.concurrency": "2",
				}, 1),
			)
		})
	})

	// This section tests parsing and validation of instance tagging configuration
	Describe("The ParseAndValidate function with default or additional instance tags", func() {

		When("parsing valid instance tag configurations", func() {
			It("should parse default instance tag successfully", func(ctx SpecContext) {
				data := map[string]string{
					DefaultInstanceTag: "prod-tag",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.DefaultInstanceTag).Should(Equal("prod-tag"))
			})

			It("should parse single additional instance tag successfully", func(ctx SpecContext) {
				data := map[string]string{
					AdditionalInstanceTags: "env=production",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.AdditionalInstanceTags).Should(HaveKeyWithValue("env", "production"))
			})

			It("should parse multiple additional instance tags successfully", func(ctx SpecContext) {
				data := map[string]string{
					AdditionalInstanceTags: "env=production,team=backend,cost-center=engineering",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.AdditionalInstanceTags).Should(HaveKeyWithValue("env", "production"))
				Expect(config.AdditionalInstanceTags).Should(HaveKeyWithValue("team", "backend"))
				Expect(config.AdditionalInstanceTags).Should(HaveKeyWithValue("cost-center", "engineering"))
			})

			It("should parse additional instance tags with whitespace successfully", func(ctx SpecContext) {
				data := map[string]string{
					AdditionalInstanceTags: "env=production , team=backend , cost-center=engineering",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.AdditionalInstanceTags).Should(HaveLen(3))
			})

			It("should split tag values at first equals sign only", func(ctx SpecContext) {
				data := map[string]string{
					AdditionalInstanceTags: "equation=a",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.AdditionalInstanceTags).Should(HaveKeyWithValue("equation", "a"))
			})

			It("should return empty tags when fields are not present", func(ctx SpecContext) {
				data := map[string]string{}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.DefaultInstanceTag).Should(BeEmpty())
				Expect(config.AdditionalInstanceTags).Should(BeEmpty())
			})

			It("should return empty tags when fields are empty strings", func(ctx SpecContext) {
				data := map[string]string{
					DefaultInstanceTag:     "",
					AdditionalInstanceTags: "",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.DefaultInstanceTag).Should(BeEmpty())
				Expect(config.AdditionalInstanceTags).Should(BeEmpty())
			})
		})

		When("parsing invalid instance tag configurations", func() {
			It("should return error for tag without equals sign", func(ctx SpecContext) {
				data := map[string]string{
					AdditionalInstanceTags: "invalid-tag",
				}

				_, err := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(err.Error()).Should(ContainSubstring("invalid additional instance tag format 'invalid-tag'"))
			})

			It("should accept tag with only key and empty value", func(ctx SpecContext) {
				data := map[string]string{
					AdditionalInstanceTags: "key=",
				}

				config, _ := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(config.AdditionalInstanceTags).Should(HaveKeyWithValue("key", ""))
			})

			It("should return error when one tag in list is invalid", func(ctx SpecContext) {
				data := map[string]string{
					AdditionalInstanceTags: "env=production,invalid-tag,team=backend",
				}

				_, err := ParseAndValidate(ctx, nil, data, systemNamespace)
				Expect(err.Error()).Should(ContainSubstring("invalid additional instance tag format 'invalid-tag'"))
			})
		})
	})

	// This section tests a comprehensive scenario, combining multiple platform types, generated using a host-config.yml
	// taken from stone-prod-p02 before Maxim got to it )))) for extra-reality
	Describe("The ParseAndValidate function with mixed platform configurations", func() {

		When("parsing configurations with all platform types", func() {
			It("should parse the production host-config.yaml successfully", func(ctx SpecContext) {
				// using the same pattern as main->streamFileYamlToTektonObj()
				bytes, err := os.ReadFile(filepath.Clean("../../../testing/sources/host-config.yaml"))
				Expect(err).ShouldNot(HaveOccurred(), "Failed to read host-config.yaml")

				scheme := runtime.NewScheme()
				utilruntime.Must(corev1.AddToScheme(scheme))
				codecFactory := serializer.NewCodecFactory(scheme)
				decoder := codecFactory.UniversalDecoder(corev1.SchemeGroupVersion)
				configMap := &corev1.ConfigMap{}
				err = runtime.DecodeInto(decoder, bytes, configMap)
				Expect(err).ShouldNot(HaveOccurred(), "Failed to decode host-config.yaml")
				config, err := ParseAndValidate(ctx, nil, configMap.Data, systemNamespace)
				Expect(err).ShouldNot(HaveOccurred(), "Failed to parse production host-config")

				// Verify local platforms
				Expect(config.LocalPlatforms).Should(ContainElements("linux/x86_64", "local", "localhost"))

				// Verify dynamic platforms using a few key plafoms
				Expect(config.DynamicPlatforms).Should(HaveKey("linux/arm64"))
				Expect(config.DynamicPlatforms).Should(HaveKey("linux/amd64"))
				Expect(config.DynamicPlatforms["linux/arm64"].Type).Should(Equal("aws"))
				Expect(config.DynamicPlatforms["linux/arm64"].MaxInstances).Should(Equal(250))
				Expect(config.DynamicPlatforms["linux/arm64"].InstanceTag).Should(Equal("prod-arm64"))

				// Verify static platforms - count hosts by platform type
				s390xCount := 0
				ppc64leCount := 0
				for _, host := range config.StaticPlatforms {
					switch host.Platform {
					case "linux/s390x":
						s390xCount++
					case "linux/ppc64le":
						ppc64leCount++
					}
				}
				Expect(s390xCount).Should(Equal(10))
				Expect(ppc64leCount).Should(Equal(5))

				// Verify instance tags
				Expect(config.DefaultInstanceTag).Should(Equal("rhtap-prod"))
				Expect(config.AdditionalInstanceTags).Should(HaveLen(6))
				Expect(config.AdditionalInstanceTags).Should(HaveKeyWithValue("Project", "Konflux"))
				Expect(config.AdditionalInstanceTags).Should(HaveKeyWithValue("cost-center", "666"))
			})
		})
	})
})
