package aws

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This test is only here to check AWS connectivity in a very primitive and quick way until KFLUXINFRA-1065
// work starts
var _ = Describe("Ec2 Connection Test", func() {

	Describe("Testing pingSSHIp", func() {
		DescribeTable("Testing the ability to ping via SSH a remote AWS ec2 instance",
			func(testInstanceIP string, shouldFail bool) {

				ec2IPAddress, err := pingSSHIp(context.TODO(), testInstanceIP)

				if !shouldFail {
					Expect(err).Should(BeNil())
					Expect(testInstanceIP).Should(Equal(ec2IPAddress))
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

	Describe("Testing SshUser", func() {
		It("The simplest damn test", func() {
			var awsTestInstance AwsDynamicConfig
			sshUser := awsTestInstance.SshUser()

			Expect(sshUser).Should(Equal("ec2-user"))
		})
	})
})
