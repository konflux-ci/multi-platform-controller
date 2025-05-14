package aws

import (
	"encoding/base64"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var _ = Describe("AWS EC2 Helper Functions", func() {
	DescribeTable("Find VM instances linked to non-existent TaskRuns",
		func(log logr.Logger, ec2Reservations []types.Reservation, existingTaskRuns map[string][]string, expectedInstances []string) {
			cfg := AWSEc2DynamicConfig{}
			Expect(cfg.findInstancesWithoutTaskRuns(log, ec2Reservations, existingTaskRuns)).To(Equal(expectedInstances))
		},
		Entry("no reservations", logr.Discard(),
			[]types.Reservation{}, map[string][]string{},
			nil,
		),
		Entry("no instances", logr.Discard(),
			[]types.Reservation{{Instances: []types.Instance{}}},
			map[string][]string{},
			nil,
		),
		Entry("instance w/ no tags", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{InstanceId: aws.String("id"), Tags: []types.Tag{}},
				}},
			},
			map[string][]string{},
			[]string{"id"},
		),
		Entry("instance w/ no TaskRun ID tag", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("id"),
						Tags:       []types.Tag{{Key: aws.String("key"), Value: aws.String("value")}},
					},
				}},
			},
			map[string][]string{},
			[]string{"id"},
		),
		Entry("instance w/ invalid TaskRun ID", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("id"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("value")}},
					},
				}},
			},
			map[string][]string{},
			[]string{"id"},
		),
		Entry("all instances have existing TaskRuns", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("task1"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task1")}},
					},
					{
						InstanceId: aws.String("task2"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task2")}},
					},
					{
						InstanceId: aws.String("task3"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task3")}},
					},
					{
						InstanceId: aws.String("task4"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task4")}},
					},
				}},
			},
			map[string][]string{"test": {"task1", "task2", "task3", "task4"}},
			nil,
		),
		Entry("one instance doesn't have a TaskRun", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("task-a"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task-a")}},
					},
					{
						InstanceId: aws.String("task2"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task2")}},
					},
					{
						InstanceId: aws.String("task3"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task3")}},
					},
					{
						InstanceId: aws.String("task4"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task4")}},
					},
				}},
			},
			map[string][]string{"test": {"task1", "task2", "task3", "task4"}},
			[]string{"task-a"},
		),
		Entry("multiple instances don't have a TaskRun", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("task-a"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task-a")}},
					},
					{
						InstanceId: aws.String("task-b"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("")}},
					},
					{
						InstanceId: aws.String("task3"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task3")}},
					},
					{
						InstanceId: aws.String("task4"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task4")}},
					},
				}},
			},
			map[string][]string{"test": {"task1", "task2", "task3", "task4"}},
			[]string{"task-a", "task-b"}),
		Entry("no instances have a TaskRun", logr.Discard(),
			[]types.Reservation{
				{Instances: []types.Instance{
					{
						InstanceId: aws.String("task1"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task1")}},
					},
					{
						InstanceId: aws.String("task2"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task2")}},
					},
					{
						InstanceId: aws.String("task3"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task3")}},
					},
					{
						InstanceId: aws.String("task4"),
						Tags:       []types.Tag{{Key: aws.String(cloud.TaskRunTagKey), Value: aws.String("test:task4")}},
					},
				}},
			},
			map[string][]string{"test-namespace": {"task1", "task2", "task3", "task4"}},
			[]string{"task1", "task2", "task3", "task4"}),
	)

	// This test is only here to check AWS connectivity in a very primitive and quick way until assertion testing
	// work starts
	Describe("Testing pingIpAddress", func() {
		DescribeTable("Testing the ability to ping a remote AWS ec2 instance via SSH",
			func(testInstanceIP string, shouldFail bool) {

				err := pingIPAddress(testInstanceIP)

				if !shouldFail {
					Expect(err).Should(BeNil())
				} else {
					Expect(err).Should(HaveOccurred())
				}

			},
			Entry("Positive test - IP address", "150.239.19.36", false),
			Entry("Negative test - no such IP address", "192.168.4.231", true),
			Entry("Negative test - no such DNS name", "not a DNS name, that's for sure", true),
			Entry("Negative test - not an IP address", "Not an IP address", true),
		)
	})

	Describe("configureInstance", func() {
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

		Context("when configuring basic instance details", func() {
			It("should correctly set KeyName, AMI, and InstanceType", func() {
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.KeyName).To(PointTo(Equal(ecConfig.KeyName)))
				Expect(runInput.ImageId).To(PointTo(Equal(ecConfig.Ami)))
				Expect(runInput.InstanceType).To(Equal(types.InstanceType(ecConfig.InstanceType)))
			})

			It("should set MinCount=1, MaxCount=1, EbsOptimized=true, and ShutdownBehavior=terminate", func() {
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.MinCount).To(PointTo(Equal(int32(1))))
				Expect(runInput.MaxCount).To(PointTo(Equal(int32(1))))
				Expect(runInput.EbsOptimized).To(PointTo(BeTrue()))
				Expect(runInput.InstanceInitiatedShutdownBehavior).To(Equal(types.ShutdownBehaviorTerminate))
			})
		})

		Context("when configuring networking", func() {
			DescribeTable("with various subnet and security group settings",
				// This DescribeTable takes an ecConfig-mutating function and a function that verifies the new content
				// of the ecConfig after going through configureInstance
				func(setupConfig func(conf *AWSEc2DynamicConfig), verify func(input *ec2.RunInstancesInput)) {
					currentConfig := ecConfig
					setupConfig(&currentConfig)
					runInput := currentConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					verify(runInput)
				},
				Entry("using baseline SubnetId and SecurityGroupId",
					func(conf *AWSEc2DynamicConfig) {
						// ecConfig is as it's created in BeforeEach.
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.SubnetId).To(PointTo(Equal(ecConfig.SubnetId)))
						Expect(input.SecurityGroupIds).To(ContainElement(ecConfig.SecurityGroupId))
						Expect(input.SecurityGroups).To(BeNil())
					},
				),
				Entry("with only SecurityGroup name (clearing SG ID from baseline)",
					func(conf *AWSEc2DynamicConfig) {
						conf.SecurityGroup = "sg-name-example"
						conf.SecurityGroupId = ""
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.SecurityGroups).To(ContainElement("sg-name-example"))
						Expect(input.SecurityGroupIds).To(BeNil())
					},
				),
				Entry("with no network settings (clearing baseline)",
					func(conf *AWSEc2DynamicConfig) {
						conf.SubnetId = ""
						conf.SecurityGroupId = ""
						conf.SecurityGroup = ""
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.SubnetId).To(BeNil())
						Expect(input.SecurityGroups).To(BeNil())
						Expect(input.SecurityGroupIds).To(BeNil())
					},
				),
			)
		})

		Context("when configuring IAM instance profile", func() {
			DescribeTable("with various IAM profile settings",
				// This DescribeTable also takes an ecConfig-mutating function and a function that verifies the new
				//content of the ecConfig after going through configureInstance
				func(setupConfig func(conf *AWSEc2DynamicConfig), verify func(input *ec2.RunInstancesInput)) {
					currentConfig := ecConfig
					setupConfig(&currentConfig)
					runInput := currentConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					verify(runInput)
				},
				Entry("using baseline InstanceProfileArn",
					func(conf *AWSEc2DynamicConfig) {
						// ecConfig ia as it's created in BeforeEach.
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.IamInstanceProfile).NotTo(BeNil())
						Expect(input.IamInstanceProfile.Arn).To(PointTo(Equal(ecConfig.InstanceProfileArn)))
						Expect(input.IamInstanceProfile.Name).To(BeNil())
					},
				),
				Entry("with only InstanceProfileName (clearing ARN from baseline)",
					func(conf *AWSEc2DynamicConfig) {
						conf.InstanceProfileName = "profile-name-example"
						conf.InstanceProfileArn = ""
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.IamInstanceProfile).NotTo(BeNil())
						Expect(input.IamInstanceProfile.Name).To(PointTo(Equal("profile-name-example")))
						Expect(input.IamInstanceProfile.Arn).To(BeNil())
					},
				),
				Entry("with no IAM profile settings (clearing baseline)",
					func(conf *AWSEc2DynamicConfig) {
						conf.InstanceProfileName = ""
						conf.InstanceProfileArn = ""
					},
					func(input *ec2.RunInstancesInput) { Expect(input.IamInstanceProfile).To(BeNil()) },
				),
			)
		})

		Context("when configuring spot instances", func() {
			It("should configure InstanceMarketOptions if MaxSpotInstancePrice is set", func() {
				ecConfig.MaxSpotInstancePrice = "0.075"
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.InstanceMarketOptions).NotTo(BeNil())
				Expect(runInput.InstanceMarketOptions.MarketType).To(Equal(types.MarketTypeSpot))
				Expect(runInput.InstanceMarketOptions.SpotOptions).NotTo(BeNil())
				Expect(runInput.InstanceMarketOptions.SpotOptions.MaxPrice).To(PointTo(Equal("0.075")))
				Expect(runInput.InstanceMarketOptions.SpotOptions.InstanceInterruptionBehavior).To(Equal(types.InstanceInterruptionBehaviorTerminate))
				Expect(runInput.InstanceMarketOptions.SpotOptions.SpotInstanceType).To(Equal(types.SpotInstanceTypeOneTime))
			})

			It("should not set InstanceMarketOptions if MaxSpotInstancePrice is empty", func() {
				ecConfig.MaxSpotInstancePrice = ""
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.InstanceMarketOptions).To(BeNil())
			})
		})

		Context("when configuring tags", func() {
			It("should include default and Name tags correctly with baseline config", func() {
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.TagSpecifications).To(HaveLen(1))
				Expect(runInput.TagSpecifications[0].ResourceType).To(Equal(types.ResourceTypeInstance))
				tags := runInput.TagSpecifications[0].Tags
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal(MultiPlatformManaged))), HaveField("Value", PointTo(Equal("true"))))))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal(cloud.InstanceTag))), HaveField("Value", PointTo(Equal(instanceTag))))))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal("Name"))), HaveField("Value", PointTo(Equal("multi-platform-builder-"+taskRunName))))))
			})

			It("should include default and additional tags when provided", func() {
				additionalTags["CostCenter"] = "AlphaTeam"
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				tags := runInput.TagSpecifications[0].Tags
				Expect(tags).To(HaveLen(3 + len(additionalTags)))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal("CostCenter"))), HaveField("Value", PointTo(Equal("AlphaTeam"))))))
			})
		})

		Context("when configuring EBS block device mappings", func() {
			It("should set EBS properties correctly based on baseline config", func() {
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.BlockDeviceMappings).To(HaveLen(1))
				bdMapping := runInput.BlockDeviceMappings[0]
				Expect(bdMapping.DeviceName).To(PointTo(Equal("/dev/sda1")))
				Expect(bdMapping.Ebs).NotTo(BeNil())
				Expect(bdMapping.Ebs.DeleteOnTermination).To(PointTo(BeTrue()))
				Expect(bdMapping.Ebs.VolumeSize).To(PointTo(Equal(ecConfig.Disk)))
				Expect(bdMapping.Ebs.VolumeType).To(Equal(types.VolumeTypeGp3))
			})

			DescribeTable("with various IOPS and Throughput settings",
				func(iops *int32, throughput *int32) {
					ecConfig.Iops = iops
					ecConfig.Throughput = throughput
					runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					ebsDevice := runInput.BlockDeviceMappings[0].Ebs
					if iops == nil {
						Expect(ebsDevice.Iops).To(BeNil())
					} else {
						Expect(ebsDevice.Iops).To(PointTo(Equal(*iops)))
					}
					if throughput == nil {
						Expect(ebsDevice.Throughput).To(BeNil())
					} else {
						Expect(ebsDevice.Throughput).To(PointTo(Equal(*throughput)))
					}
				},
				Entry("using baseline IOPS and Throughput", newDefaultValidEC2ConfigForInstance().Iops, newDefaultValidEC2ConfigForInstance().Throughput),
				Entry("with custom IOPS and Throughput", aws.Int32(5000), aws.Int32(300)),
				Entry("with only IOPS set (clearing baseline Throughput)", aws.Int32(5500), nil),
				Entry("with only Throughput set (clearing baseline Iops)", nil, aws.Int32(350)),
				Entry("with neither IOPS nor Throughput set (clearing baseline for both)", nil, nil),
			)
		})

		Context("when UserData is provided (already base64 encoded)", func() {
			It("should pass it through to RunInstancesInput using encoded commonUserData", func() {
				// commonUserData is from aws_test.go (raw script)
				encodedCommonUserData := base64.StdEncoding.EncodeToString([]byte(commonUserData))
				ecConfig.UserData = &encodedCommonUserData
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.UserData).To(PointTo(Equal(encodedCommonUserData)))
			})

			It("should be nil if ecConfig.UserData is set to nil (overriding baseline if it had UserData)", func() {
				ecConfig.UserData = nil
				runInput := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.UserData).To(BeNil())
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
