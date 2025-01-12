package aws

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This test is only here to check SeaLights integration, it will be replaced with a more robust integration
// test for the AWS API
var _ = Describe("Ec2 Connection Test", func() {

	Describe("Testing pingSSHIp", func() {
		DescribeTable("Testing the ability to ping via SSH a remote AWS ec2 instance",
			func(testInstanceIP string, shouldFail bool) {
				Skip("Skipping test because VM setup is missing so its too fragile")

				ec2IPAddress, err := pingSSHIp(context.TODO(), testInstanceIP)

				if !shouldFail {
					Expect(err).Should(BeNil())
					Expect(testInstanceIP).Should(Equal(ec2IPAddress))
				} else {
					Expect(err).Should(HaveOccurred())
				}

			},
			Entry("Positive test - IP address", "3.87.91.87", false),
			Entry("Positive test - DNS name", "ec2-3-87-91-87.compute-1.amazonaws.com", false),
			Entry("Negative test - no such IP address", "192.168.4.231", true),
			Entry("Negative test - no such DNS name", "not a DNS name, that's for sure", true),
			Entry("Negative test - not an IP address", "Not an IP address", true),
		)
	})

	Describe("Testing SshUser", func() {
		It("The simplest damn test", func() {
			var awsTestInstance AwsDynamicConfig
			sshUser := awsTestInstance.SshUser()

			Expect(sshUser).Should(Equal("ec2-user"))
		})
	})
})
