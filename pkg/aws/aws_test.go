package aws

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Ec2 Connection Test", func() {

	// This test is only here to check SeaLights integration, it will be replaced with a more robust integration
	// test for the AWS API
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
		Entry("Positive test - IP address", "3.87.91.87", false),
		Entry("Positive test - DNS name", "ec2-3-87-91-87.compute-1.amazonaws.com", false),
		Entry("Negative test - no such IP address", "192.168.4.231", true),
		Entry("Negative test - no such DNS name", "not a DNS name, that's for sure", true),
		Entry("Negative test - not an IP address", "Not an IP address", true),
	)
})
