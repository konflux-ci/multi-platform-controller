package aws

import (
	"encoding/base64"

	"github.com/aws/aws-sdk-go-v2/aws"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const systemNamespace = "multi-platform-controller"

func stringEncode(s *string) *string {
	base54val := base64.StdEncoding.EncodeToString([]byte(*s))
	return &base54val
}

var _ = Describe("Ec2 Unit Test Suit", func() {

	// Testing the provider for AwsDynamicConfig with 4 test cases:
	// 	1. A positive test for valid configuarion fields
	// 	2. A negative test with a platform name that does not exist
	//	3. A negative test with empty configuration values
	//	4. A negative test with configuration values that do not match the expected data type - non-numeric content of config string
	Describe("Testing Ec2Provider", func() {

		DescribeTable("Testing the creation of AwsDynamicConfig properly no matter the values",
			func(platformName string, testConfig map[string]string, expectedDisk int, expectedIops *int32, expectedThroughput *int32, expectedUserData *string) {
				config := map[string]string{
					"dynamic." + platformName + ".region":                "test-region",
					"dynamic." + platformName + ".ami":                   "test-ami",
					"dynamic." + platformName + ".instance-type":         "test-instance-type",
					"dynamic." + platformName + ".key-name":              "test-key-name",
					"dynamic." + platformName + ".aws-secret":            "test-secret",
					"dynamic." + platformName + ".security-group":        "test-security-group",
					"dynamic." + platformName + ".security-group-id":     "test-security-group-id",
					"dynamic." + platformName + ".subnet-id":             "test-subnet-id",
					"dynamic." + platformName + ".spot-price":            "test-spot-price",
					"dynamic." + platformName + ".instance-profile-name": "test-instance-profile-name",
					"dynamic." + platformName + ".instance-profile-arn":  "test-instance-profile-arn",
					"dynamic." + platformName + ".disk":                  testConfig["disk"],
					"dynamic." + platformName + ".iops":                  testConfig["iops"],
					"dynamic." + platformName + ".throughput":            testConfig["throughput"],
					"dynamic." + platformName + ".user-data":             testConfig["user-data"],
				}
				provider := CreateEc2CloudConfig(platformName, config, systemNamespace)
				Expect(provider).ToNot(BeNil())
				providerConfig := provider.(AWSEc2DynamicConfig)
				Expect(providerConfig).ToNot(BeNil())

				Expect(providerConfig.Region).To(Equal("test-region"))
				Expect(providerConfig.Ami).To(Equal("test-ami"))
				Expect(providerConfig.InstanceType).To(Equal("test-instance-type"))
				Expect(providerConfig.KeyName).To(Equal("test-key-name"))
				Expect(providerConfig.Secret).To(Equal("test-secret"))
				Expect(providerConfig.SecurityGroup).To(Equal("test-security-group"))
				Expect(providerConfig.SecurityGroupId).To(Equal("test-security-group-id"))
				Expect(providerConfig.SubnetId).To(Equal("test-subnet-id"))
				Expect(providerConfig.MaxSpotInstancePrice).To(Equal("test-spot-price"))
				Expect(providerConfig.InstanceProfileName).To(Equal("test-instance-profile-name"))
				Expect(providerConfig.InstanceProfileArn).To(Equal("test-instance-profile-arn"))
				Expect(providerConfig.Disk).To(Equal(int32(expectedDisk)))
				Expect(providerConfig.Iops).To(Equal(expectedIops))
				Expect(providerConfig.Throughput).To(Equal(expectedThroughput))
				Expect(providerConfig.UserData).Should(SatisfyAny(Equal(stringEncode(expectedUserData)), BeNil()))
			},
			Entry("Positive - valid config map keys", "linux-largecpu-x86_64", map[string]string{
				"disk":       "200",
				"iops":       "100",
				"throughput": "50",
				"user-data":  commonUserData},
				200, aws.Int32(100), aws.Int32(50), aws.String(commonUserData)),
			Entry("Negative - nonexistant platform name", "koko-hazamar", map[string]string{
				"disk":       "200",
				"iops":       "100",
				"throughput": "50",
				"user-data":  commonUserData},
				200, aws.Int32(100), aws.Int32(50), aws.String(commonUserData)),
			Entry("Negative - missing config data", "linux-c4xlarge-arm64", map[string]string{
				"disk":       "",
				"iops":       "",
				"throughput": "",
				"user-data":  ""},
				40, nil, nil, aws.String("")),
			Entry("Negative - config data with bad data types", "linux-g6xlarge-amd64", map[string]string{
				"disk":       "koko-hazamar",
				"iops":       "koko-hazamar",
				"throughput": "koko-hazamar",
				"user-data":  commonUserData},
				40, nil, nil, aws.String(commonUserData)),
		)
	})

	Describe("Testing SshUser", func() {
		It("The simplest damn test", func() {
			var awsTestInstance AWSEc2DynamicConfig
			sshUser := awsTestInstance.SshUser()

			Expect(sshUser).Should(Equal("ec2-user"))
		})
	})
})

var commonUserData = `|-
Content-Type: multipart/mixed; boundary="//"
MIME-Version: 1.0
  
--//
Content-Type: text/cloud-config; charset="us-ascii"
MIME-Version: 1.0
Content-Transfer-Encoding: 7bit
Content-Disposition: attachment; filename="cloud-config.txt"

#cloud-config
cloud_final_modules:
  - [scripts-user, always]
  
--//
Content-Type: text/x-shellscript; charset="us-ascii"
MIME-Version: 1.0
Content-Transfer-Encoding: 7bit
Content-Disposition: attachment; filename="userdata.txt"

#!/bin/bash -ex
  
if lsblk -no FSTYPE /dev/nvme1n1 | grep -qE "\S"; then
 echo "File system exists on the disk."
else
 echo "No file system found on the disk /dev/nvme1n1"
 mkfs -t xfs /dev/nvme1n1
fi

mount /dev/nvme1n1 /home

if [ -d "/home/var-lib-containers" ]; then
 echo "Directory "/home/var-lib-containers" exist"
else
 echo "Directory "/home/var-lib-containers" doesn|t exist"
 mkdir -p /home/var-lib-containers /var/lib/containers
fi

mount --bind /home/var-lib-containers /var/lib/containers

if [ -d "/home/ec2-user" ]; then
echo "ec2-user home exists"
else
echo "ec2-user home doesnt exist"
mkdir -p /home/ec2-user/.ssh
chown -R ec2-user /home/ec2-user
fi

sed -n "s,.*\(ssh-.*\s\),\1,p" /root/.ssh/authorized_keys > /home/ec2-user/.ssh/authorized_keys
chown ec2-user /home/ec2-user/.ssh/authorized_keys
chmod 600 /home/ec2-user/.ssh/authorized_keys
chmod 700 /home/ec2-user/.ssh
restorecon -r /home/ec2-user

--//--`
