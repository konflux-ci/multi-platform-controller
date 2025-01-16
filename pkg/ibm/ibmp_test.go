package ibm

import (
	"encoding/base64"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const systemNamespace = "multi-platform-controller"

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	Expect(err).NotTo(HaveOccurred())
	return f
}

var _ = DescribeTable("IBMPowerProvider unit test",
	func(platform string, testConfig map[string]string, expectedCores string, expectedMemory string, expectedDisk string) {
		config := map[string]string{
			"dynamic." + platform + ".key":       "test-key",
			"dynamic." + platform + ".image":     "test-image",
			"dynamic." + platform + ".secret":    "test-secret",
			"dynamic." + platform + ".url":       "test-url",
			"dynamic." + platform + ".crn":       "test-crn",
			"dynamic." + platform + ".network":   "test-network",
			"dynamic." + platform + ".system":    "test-system",
			"dynamic." + platform + ".user-data": "test-userData",
			"dynamic." + platform + ".memory":    testConfig["memory"],
			"dynamic." + platform + ".cores":     testConfig["cores"],
			"dynamic." + platform + ".disk":      testConfig["disk"]}

		provider := IBMPowerProvider(platform, config, systemNamespace)
		Expect(provider).ToNot(BeNil())
		providerConfig := provider.(IBMPowerDynamicConfig)
		Expect(providerConfig).ToNot(BeNil())

		Expect(providerConfig.Key).To(Equal("test-key"))
		Expect(providerConfig.Image).To(Equal("test-image"))
		Expect(providerConfig.Secret).To(Equal("test-secret"))
		Expect(providerConfig.Url).To(Equal("test-url"))
		Expect(providerConfig.CRN).To(Equal("test-crn"))
		Expect(providerConfig.Network).To(Equal("test-network"))
		Expect(providerConfig.System).To(Equal("test-system"))
		Expect(providerConfig.UserData).To(Equal(base64.StdEncoding.EncodeToString([]byte("test-userData"))))
		Expect(providerConfig.Cores).To(Equal(parseFloat(expectedCores)))
		Expect(providerConfig.Memory).To(Equal(parseFloat(expectedMemory)))
		Expect(providerConfig.Disk).To(Equal(parseFloat(expectedDisk)))
		Expect(providerConfig.SystemNamespace).To(Equal(systemNamespace))
	},

	Entry("Positive - valid config map keys", "power-rhtap-prod-2", map[string]string{
		"memory": "10.0",
		"cores":  "2.0",
		"disk":   "100"}, "2.0", "10.0", "100"),
	Entry("Negative - nonexistant platform name", "koko-hazamar", map[string]string{
		"memory": "10.0",
		"cores":  "2.0",
		"disk":   "300"}, "2.0", "10.0", "300"),
	Entry("Negative - missing config data", "ppc6", map[string]string{
		"memory": "",
		"cores":  "",
		"disk":   ""}, "0.25", "2", "100"),
	Entry("Negative - non-numeral config data", "ppc6", map[string]string{
		"memory": "koko-hazamar",
		"cores":  "koko-hazamar",
		"disk":   "koko-hazamar"}, "0.25", "2", "100"),
)
