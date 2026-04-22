package aws

import (
	"encoding/base64"

	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("AWS EC2 Helper Functions", func() {
	Describe("validateIPAddress", func() {
		ecConfig := AWSEc2DynamicConfig{}

		It("should return error when instance has no IP addresses or DNS name", func(ctx SpecContext) {
			instance := &types.Instance{
				InstanceId:       aws.String("koko_hazamar"),
				PrivateIpAddress: nil,
				PublicIpAddress:  nil,
				PublicDnsName:    nil,
			}

			ip, err := ecConfig.validateIPAddress(ctx, instance)

			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("has no accessible IP address"))
			Expect(err.Error()).Should(ContainSubstring("koko_hazamar"))
			Expect(ip).Should(BeEmpty())
		})
	})

	// This test is only here to check AWS connectivity in a very primitive and quick way until assertion testing
	// work starts
	Describe("Testing pingIpAddress", func() {
		DescribeTable("Testing the ability to ping a remote AWS ec2 instance via SSH",
			func(testInstanceIP string, shouldFail bool) {

				err := pingIPAddress(testInstanceIP)

				if !shouldFail {
					Expect(err).ShouldNot(HaveOccurred())
				} else {
					Expect(err).Should(HaveOccurred())
				}

			},
			Entry("Positive test - IP address", "169.48.19.34", false),
			Entry("Negative test - no such IP address", "192.168.4.231", true),
			Entry("Negative test - no such DNS name", "not a DNS name, that's for sure", true),
			Entry("Negative test - not an IP address", "Not an IP address", true),
		)
	})
	// A unit test for configureInstance. Bypasses CreateEc2CloudConfig's logic to isolate cases where its logic may be
	// faulty, hence the helper function newDefaultValidEC2ConfigForInstance which provides a neutral all-good
	// AWSEc2DynamicConfig with baseline and passing generic configurations.
	// Breaks the various configuration fields to families of configuration topics, each tested in it own Context. Some
	// have happy test paths, some have sad ones.
	// Assisted-by: Gemini
	Describe("Testing configureInstance", func() {
		var (
			ecConfig       AWSEc2DynamicConfig
			taskRunName    string
			instanceTag    string
			additionalTags map[string]string
		)

		BeforeEach(func() {
			taskRunName = "cfg-taskrun"
			instanceTag = "cfg-instance"
			additionalTags = make(map[string]string)
			ecConfig = newDefaultValidEC2ConfigForInstance()
		})

		When("configuring basic instance details", func() {
			It("should correctly set KeyName, AMI, and InstanceType", func() {
				runInput, err := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(runInput.KeyName).To(Equal(&ecConfig.KeyName))
				Expect(runInput.ImageId).To(Equal(&ecConfig.Ami))
				Expect(runInput.InstanceType).To(Equal(types.InstanceType(ecConfig.InstanceType)))
			})

			It("should set MinCount, MaxCount, EbsOptimized, ShutdownBehavior with valid and default properties",
				func() {
					runInput, err := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					Expect(err).ShouldNot(HaveOccurred())
					By("Verifying the current specific values")
					Expect(runInput.MinCount).To(Equal(aws.Int32(1)))
					Expect(runInput.MaxCount).To(Equal(aws.Int32(1)))
					Expect(runInput.EbsOptimized).To(PointTo(BeTrue()))
					Expect(runInput.InstanceInitiatedShutdownBehavior).To(Equal(types.ShutdownBehaviorTerminate))

					By("verifying properties of instance counts")
					Expect(runInput.MinCount).NotTo(BeNil())
					Expect(runInput.MaxCount).NotTo(BeNil())
					Expect(*runInput.MinCount).To(BeNumerically(">", 0),
						"MinCount should be positive")
					Expect(*runInput.MaxCount).To(BeNumerically(">=", *runInput.MinCount),
						"MaxCount should be greater than or equal to MinCount")
				})
		})

		When("configuring networking", func() {
			DescribeTable("with various subnet and security group settings",
				// This DescribeTable takes an ecConfig-mutating function and a function that verifies the new content
				// of the ecConfig after going through configureInstance
				func(setupConfig func() AWSEc2DynamicConfig, verify func(input *ec2.RunInstancesInput)) {
					currentConfig := setupConfig()
					runInput, _ := currentConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					verify(runInput)
				},
				Entry("using baseline SubnetId and SecurityGroupId",
					func() AWSEc2DynamicConfig { return newDefaultValidEC2ConfigForInstance() },
					func(input *ec2.RunInstancesInput) {
						Expect(input.SubnetId).To(Equal(&ecConfig.SubnetId))
						Expect(input.SecurityGroupIds).To(ContainElement(ecConfig.SecurityGroupId))
						Expect(input.SecurityGroups).To(BeNil())
					},
				),
				Entry("with only SecurityGroup name (clearing SG ID from baseline)",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.SecurityGroup = "sg-name-example"
						conf.SecurityGroupId = ""
						return conf
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.SecurityGroups).To(ContainElement("sg-name-example"))
						Expect(input.SecurityGroupIds).To(BeNil())
					},
				),
				Entry("with no network settings (clearing baseline)",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.SubnetId = ""
						conf.SecurityGroupId = ""
						conf.SecurityGroup = ""
						return conf
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.SubnetId).To(BeNil())
						Expect(input.SecurityGroups).To(BeNil())
						Expect(input.SecurityGroupIds).To(BeNil())
					},
				),
			)
		})

		When("configuring IAM instance profile", func() {
			DescribeTable("with various IAM profile settings",
				// This DescribeTable also takes an ecConfig-mutating function and a function that verifies the new
				//content of the ecConfig after going through configureInstance
				func(setupConfig func() AWSEc2DynamicConfig, verify func(input *ec2.RunInstancesInput)) {
					currentConfig := setupConfig()
					runInput, _ := currentConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					verify(runInput)
				},
				Entry("using baseline InstanceProfileArn",
					func() AWSEc2DynamicConfig { return newDefaultValidEC2ConfigForInstance() },
					func(input *ec2.RunInstancesInput) {
						Expect(input.IamInstanceProfile).NotTo(BeNil())
						Expect(input.IamInstanceProfile.Arn).To(Equal(&ecConfig.InstanceProfileArn))
						Expect(input.IamInstanceProfile.Name).To(BeNil())
					},
				),
				Entry("with only InstanceProfileName (clearing ARN from baseline)",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.InstanceProfileName = "profile-name-example"
						conf.InstanceProfileArn = ""
						return conf
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.IamInstanceProfile).NotTo(BeNil())
						Expect(input.IamInstanceProfile.Name).To(PointTo(Equal("profile-name-example")))
						Expect(input.IamInstanceProfile.Arn).To(BeNil())
					},
				),
				// TODO: scenarios like missing AMI fields need to be tested properly. Not in the scope of this humble unit test
				Entry("with no IAM profile settings (clearing baseline)",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.InstanceProfileName = ""
						conf.InstanceProfileArn = ""
						return conf
					},
					func(input *ec2.RunInstancesInput) { Expect(input.IamInstanceProfile).To(BeNil()) },
				),
			)
		})

		When("configuring spot instances", func() {
			It("should configure InstanceMarketOptions if MaxSpotInstancePrice is set", func() {
				ecConfig.MaxSpotInstancePrice = "0.075"
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.InstanceMarketOptions).NotTo(BeNil())
				Expect(runInput.InstanceMarketOptions.MarketType).To(Equal(types.MarketTypeSpot))
				Expect(runInput.InstanceMarketOptions.SpotOptions).NotTo(BeNil())
				Expect(runInput.InstanceMarketOptions.SpotOptions.MaxPrice).To(PointTo(Equal("0.075")))
				Expect(runInput.InstanceMarketOptions.SpotOptions.InstanceInterruptionBehavior).To(Equal(types.InstanceInterruptionBehaviorTerminate))
				Expect(runInput.InstanceMarketOptions.SpotOptions.SpotInstanceType).To(Equal(types.SpotInstanceTypeOneTime))
			})

			It("should not set InstanceMarketOptions if MaxSpotInstancePrice is empty", func() {
				ecConfig.MaxSpotInstancePrice = ""
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.InstanceMarketOptions).To(BeNil())
			})
		})

		When("configuring tags", func() {
			It("should include default and Name tags correctly with baseline config", func() {
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.TagSpecifications).To(HaveLen(1))
				Expect(runInput.TagSpecifications[0].ResourceType).To(Equal(types.ResourceTypeInstance))
				tags := runInput.TagSpecifications[0].Tags
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal(MultiPlatformManaged))), HaveField("Value", PointTo(Equal("true"))))))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal(cloud.InstanceTag))), HaveField("Value", Equal(&instanceTag)))))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal("Name"))), HaveField("Value", PointTo(Equal("multi-platform-builder-"+taskRunName))))))
			})

			It("should include default and additional tags when provided", func() {
				additionalTags["CostCenter"] = "AlphaTeam"
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				tags := runInput.TagSpecifications[0].Tags
				Expect(tags).To(HaveLen(3 + len(additionalTags)))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal("CostCenter"))), HaveField("Value", PointTo(Equal("AlphaTeam"))))))
			})
		})

		When("configuring EBS block device mappings", func() {
			It("should set EBS properties correctly based on baseline config", func() {
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.BlockDeviceMappings).To(HaveLen(1))
				bdMapping := runInput.BlockDeviceMappings[0]
				Expect(bdMapping.DeviceName).To(PointTo(Equal("/dev/sda1")))
				Expect(bdMapping.Ebs).NotTo(BeNil())
				Expect(bdMapping.Ebs.DeleteOnTermination).To(PointTo(BeTrue()))
				Expect(bdMapping.Ebs.VolumeSize).To(Equal(&ecConfig.Disk))
				Expect(bdMapping.Ebs.VolumeType).To(Equal(types.VolumeTypeGp3))
			})

			DescribeTable("with various IOPS and Throughput settings",
				func(iops *int32, throughput *int32) {
					ecConfig.Iops = iops
					ecConfig.Throughput = throughput
					runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					ebsDevice := runInput.BlockDeviceMappings[0].Ebs
					Expect(ebsDevice.Iops).To(Equal(iops))
					Expect(ebsDevice.Throughput).To(Equal(throughput))
				},
				Entry("using baseline IOPS and Throughput", newDefaultValidEC2ConfigForInstance().Iops, newDefaultValidEC2ConfigForInstance().Throughput),
				Entry("with custom IOPS and Throughput", aws.Int32(5000), aws.Int32(300)),
				Entry("with only IOPS set (clearing baseline Throughput)", aws.Int32(5500), nil),
				Entry("with only Throughput set (clearing baseline Iops)", nil, aws.Int32(350)),
				Entry("with neither IOPS nor Throughput set (clearing baseline for both)", nil, nil),
			)
		})

		When("UserData is provided (already base64 encoded)", func() {
			It("should pass it through to RunInstancesInput using encoded commonUserData", func() {
				// commonUserData is from aws_test.go (raw script)
				encodedCommonUserData := base64.StdEncoding.EncodeToString([]byte(commonUserData))
				ecConfig.UserData = &encodedCommonUserData
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.UserData).To(Equal(ecConfig.UserData))
			})

			It("should be nil if ecConfig.UserData is set to nil (overriding baseline if it had UserData)", func() {
				ecConfig.UserData = nil
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.UserData).To(Equal(ecConfig.UserData))
			})
		})

		When("configuring MacOS dedicated host instances", func() {
			It("should successfully configure when all three MacOS fields are set", func() {
				ecConfig.Tenancy = "host"
				ecConfig.HostResourceGroupArn = "arn:aws:resource-groups:us-east-1:123456789012:group/mac-hosts"
				ecConfig.LicenseConfigurationArn = "arn:aws:license-manager:us-east-1:123456789012:license-configuration:lic-macos"
				runInput, err := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(runInput.Placement).ShouldNot(BeNil())
				Expect(runInput.Placement.Tenancy).Should(Equal(types.TenancyHost))
				Expect(runInput.Placement.HostResourceGroupArn).Should(PointTo(Equal("arn:aws:resource-groups:us-east-1:123456789012:group/mac-hosts")))
				Expect(runInput.LicenseSpecifications).Should(HaveLen(1))
				Expect(runInput.LicenseSpecifications[0].LicenseConfigurationArn).Should(PointTo(Equal("arn:aws:license-manager:us-east-1:123456789012:license-configuration:lic-macos")))
			})

			DescribeTable("should return validation error when only one MacOS field is set",
				func(setupConfig func() AWSEc2DynamicConfig) {
					currentConfig := setupConfig()
					_, err := currentConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					Expect(err).Should(HaveOccurred())
					Expect(err.Error()).Should(ContainSubstring("MacOS"))
				},
				Entry("with only Tenancy set",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.Tenancy = "host"
						return conf
					},
				),
				Entry("with only HostResourceGroupArn set",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.HostResourceGroupArn = "arn:aws:resource-groups:us-east-1:123456789012:group/mac-host-group"
						return conf
					},
				),
				Entry("with only LicenseConfigurationArn set",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.LicenseConfigurationArn = "arn:aws:license-manager:us-east-1:123456789012:license-configuration:lic-abc123"
						return conf
					},
				),
			)
		})
	})

	Describe("SecretCredentialsProvider Retrieve", func() {
		var s *runtime.Scheme

		BeforeEach(func() {
			s = runtime.NewScheme()
			Expect(corev1.AddToScheme(s)).Should(Succeed())
		})

		When("kubeClient is nil", func() {
			It("should return credentials from environment variables without error", func(ctx SpecContext) {
				provider := SecretCredentialsProvider{Client: nil}

				_, err := provider.Retrieve(ctx)

				Expect(err).ShouldNot(HaveOccurred())
			})
		})

		When("kubeClient is provided", func() {
			It("should return credentials from the Kubernetes secret", func(ctx SpecContext) {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: "ns"},
					Data: map[string][]byte{
						"access-key-id":     []byte("AKID"),
						"secret-access-key": []byte("SECRET"),
						"session-token":     []byte("TOKEN"),
					},
				}
				fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
				provider := SecretCredentialsProvider{Name: "aws-creds", Namespace: "ns", Client: fakeClient}

				creds, err := provider.Retrieve(ctx)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(creds.AccessKeyID).Should(Equal("AKID"))
				Expect(creds.SecretAccessKey).Should(Equal("SECRET"))
				Expect(creds.SessionToken).Should(Equal("TOKEN"))
			})

			It("should return credentials without session token when it is absent", func(ctx SpecContext) {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: "ns"},
					Data: map[string][]byte{
						"access-key-id":     []byte("AKID"),
						"secret-access-key": []byte("SECRET"),
					},
				}
				fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
				provider := SecretCredentialsProvider{Name: "aws-creds", Namespace: "ns", Client: fakeClient}

				creds, err := provider.Retrieve(ctx)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(creds.SessionToken).Should(BeEmpty())
			})

			It("should return error when the secret does not exist", func(ctx SpecContext) {
				fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
				provider := SecretCredentialsProvider{Name: "missing", Namespace: "ns", Client: fakeClient}

				_, err := provider.Retrieve(ctx)

				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("failed to retrieve the secret"))
			})
		})
	})

})

// Helper function to create a baseline valid AWSEc2DynamicConfig for configureInstance tests
func newDefaultValidEC2ConfigForInstance() AWSEc2DynamicConfig {
	var defaultUserDataPtr *string
	// For the baseline, let's assume UserData is provided via commonUserData and thus needs encoding
	if commonUserData != "" { // commonUserData is the raw script
		encoded := base64.StdEncoding.EncodeToString([]byte(commonUserData))
		defaultUserDataPtr = &encoded
	}

	return AWSEc2DynamicConfig{
		Region:               "us-west-1", // Neutral baseline values
		Ami:                  "ami-default123",
		InstanceType:         "t2.medium",
		KeyName:              "default-key",
		Secret:               "default-secret",
		SystemNamespace:      "default-sys-namespace",
		SecurityGroupId:      "sg-default00000000000",
		SubnetId:             "subnet-default00000000",
		Disk:                 int32(40),
		MaxSpotInstancePrice: "", // Default to on-demand
		InstanceProfileArn:   "arn:aws:iam::000000000000:instance-profile/default-instance-profile",
		Iops:                 aws.Int32(3000),
		Throughput:           aws.Int32(125),
		UserData:             defaultUserDataPtr,
	}
}
