//Testing IBMPowerProvider - that provides a IBMPowerDynamicConfig for creating an IBMPowerPC machine for tasks.
// The spec checks that:
//	- Configuration data is passed to IBMPowerDynamicConfig correctly when the values are valid
//  - Default values are inserted whenever the configuration written to host-config.yaml are problematic in structure or value
//
// There are 5 test cases:
// 	1. A positive test to verify all is working correctly with valid config map keys
//	2. A negative test with a platform name unlike any the MPC covers
//	3. A negative test to verify default value completion - empty memory, core number and disk size values
//	4. A negative test to verify default value completion - non-numeric memory, core number and disk size values
//	5. A negative test to verify default value completion - Verifying disk size default number of 100 if the configuration aims for less than that

package ibm

import (
	"encoding/base64"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	Expect(err).NotTo(HaveOccurred())
	return f
}

func encodeUserData(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

var _ = DescribeTable("IBMPowerProvider unit test",
	func(platform string, testConfig map[string]string, expectedUserData string, expectedMemory string, expectedCores string, expectedDisk string) {
		config := map[string]string{
			"dynamic." + platform + ".key":       "test-key",
			"dynamic." + platform + ".image":     "test-image",
			"dynamic." + platform + ".secret":    "test-secret",
			"dynamic." + platform + ".url":       "test-url",
			"dynamic." + platform + ".crn":       "test-crn",
			"dynamic." + platform + ".network":   "test-network",
			"dynamic." + platform + ".system":    "test-system",
			"dynamic." + platform + ".user-data": testConfig["userData"],
			"dynamic." + platform + ".memory":    testConfig["memory"],
			"dynamic." + platform + ".cores":     testConfig["cores"],
			"dynamic." + platform + ".disk":      testConfig["disk"]}

		provider := CreateIbmPowerConfig(platform, config, systemNamespace)
		Expect(provider).ToNot(BeNil())
		providerConfig := provider.(IBMPowerDynamicConfig)
		Expect(providerConfig).ToNot(BeNil())

		Expect(providerConfig.Key).To(Equal("test-key"))
		Expect(providerConfig.ImageId).To(Equal("test-image"))
		Expect(providerConfig.Secret).To(Equal("test-secret"))
		Expect(providerConfig.Url).To(Equal("test-url"))
		Expect(providerConfig.CRN).To(Equal("test-crn"))
		Expect(providerConfig.Network).To(Equal("test-network"))
		Expect(providerConfig.System).To(Equal("test-system"))
		Expect(providerConfig.UserData).To(Equal(encodeUserData(expectedUserData)))
		Expect(providerConfig.Cores).To(Equal(parseFloat(expectedCores)))
		Expect(providerConfig.Memory).To(Equal(parseFloat(expectedMemory)))
		Expect(providerConfig.Disk).To(Equal(parseFloat(expectedDisk)))
		Expect(providerConfig.SystemNamespace).To(Equal(systemNamespace))
	},

	Entry("Positive - valid config map keys", "power-rhtap-prod-2", map[string]string{
		"userData": commonUserData,
		"memory":   "64.0",
		"cores":    "8.0",
		"disk":     "300"}, commonUserData, "64.0", "8.0", "300"),
	Entry("Negative - nonexistant platform name", "koko-hazamar", map[string]string{
		"userData": commonUserData,
		"memory":   "64.0",
		"cores":    "8.0",
		"disk":     "300"}, commonUserData, "64.0", "8.0", "300"),
	Entry("Negative - missing config data", "ppc6", map[string]string{
		"userData": commonUserData,
		"memory":   "",
		"cores":    "",
		"disk":     ""}, commonUserData, "2", "0.25", "100"),
	Entry("Negative - non-numeral config data", "ppc6", map[string]string{
		"userData": commonUserData,
		"memory":   "koko-hazamar",
		"cores":    "koko-hazamar",
		"disk":     "koko-hazamar"}, commonUserData, "2", "0.25", "100"),
	Entry("Negative - disk size too small", "power-rhtap-prod-2", map[string]string{
		"userData": commonUserData,
		"memory":   "64.0",
		"cores":    "8.0",
		"disk":     "42"}, commonUserData, "64.0", "8.0", "100"),
)

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
