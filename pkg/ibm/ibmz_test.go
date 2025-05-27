// Testing IBMZProvider - that provides a IBMZDynamicConfig for creating an IBM s390 machine for tasks.
// The spec checks that:
//	- Configuration data is passed to IBMZDynamicConfig correctly when the values are valid
//  - The default value for disk size is inserted whenever the configuration written to host-config.yaml is problematic in structure or value
//
// There are 4 test cases:
// 	1. A positive test to verify all is working correctly with valid config map keys
//	2. A negative test with a platform name unlike any the MPC covers
//	3. A negative test to verify default value completion - empty disk size value and private-ip values
//	4. A negative test to verify default value completion - non-numeric disk size value and non-boolean private-ip value
// Assisted-by: TAG

package ibm

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("IBMZProvider unit test",
	func(arch string, testConfig map[string]string, expectedPrivateIP bool, expectedDisk int) {
		config := map[string]string{
			"dynamic." + arch + ".region":         "test-region",
			"dynamic." + arch + ".key":            "test-key",
			"dynamic." + arch + ".subnet":         "test-subnet",
			"dynamic." + arch + ".vpc":            "test-vpc",
			"dynamic." + arch + ".security-group": "test-security-group",
			"dynamic." + arch + ".image-id":       "test-image-id",
			"dynamic." + arch + ".secret":         "test-secret",
			"dynamic." + arch + ".url":            "test-url",
			"dynamic." + arch + ".profile":        "test-profile",
			"dynamic." + arch + ".private-ip":     testConfig["private-ip"],
			"dynamic." + arch + ".disk":           testConfig["disk"],
		}
		provider := CreateIbmZCloudConfig(arch, config, systemNamespace)
		Expect(provider).ToNot(BeNil())
		providerConfig := provider.(IBMZDynamicConfig)
		Expect(providerConfig).ToNot(BeNil())

		Expect(providerConfig.Region).To(Equal("test-region"))
		Expect(providerConfig.Key).To(Equal("test-key"))
		Expect(providerConfig.Subnet).To(Equal("test-subnet"))
		Expect(providerConfig.Vpc).To(Equal("test-vpc"))
		Expect(providerConfig.ImageId).To(Equal("test-image-id"))
		Expect(providerConfig.Secret).To(Equal("test-secret"))
		Expect(providerConfig.Url).To(Equal("test-url"))
		Expect(providerConfig.Profile).To(Equal("test-profile"))
		Expect(providerConfig.PrivateIP).To(Equal(testConfig["private-ip"] == "true"))
		Expect(providerConfig.Disk).To(Equal(expectedDisk))
		Expect(providerConfig.SystemNamespace).To(Equal(systemNamespace))
	},
	Entry("Positive - valid config map keys", "linux-largecpu-s390x", map[string]string{
		"private-ip": "true",
		"disk":       "200"},
		true, 200),
	Entry("Negative - nonexistant platform name", "koko-hazamar", map[string]string{
		"private-ip": "true",
		"disk":       "200"},
		true, 200),
	Entry("Negative - missing config data", "linux-s390x", map[string]string{
		"private-ip": "",
		"disk":       ""},
		true, 100),
	Entry("Negative - config data with bad data types", "linux-large-s390x", map[string]string{
		"private-ip": "koko-hazamar",
		"disk":       "koko-hazamar"},
		true, 100),
)
