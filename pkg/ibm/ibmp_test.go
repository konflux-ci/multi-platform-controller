package ibm

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
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

var _ = Describe("IBM Power Unit Tests", func() {

	Describe("CreateIBMPowerCloudConfig", func() {
		DescribeTable("config parsing",
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

				provider := CreateIBMPowerCloudConfig(platform, config, systemNamespace)
				Expect(provider).ToNot(BeNil())
				providerConfig := provider.(IBMPowerDynamicConfig)
				Expect(providerConfig).ToNot(BeNil())

				Expect(providerConfig.Key).Should(Equal("test-key"))
				Expect(providerConfig.ImageId).Should(Equal("test-image"))
				Expect(providerConfig.Secret).Should(Equal("test-secret"))
				Expect(providerConfig.Url).Should(Equal("test-url"))
				Expect(providerConfig.CRN).Should(Equal("test-crn"))
				Expect(providerConfig.Network).Should(Equal("test-network"))
				Expect(providerConfig.System).Should(Equal("test-system"))
				Expect(providerConfig.UserData).Should(Equal(encodeUserData(expectedUserData)))
				Expect(providerConfig.Cores).Should(Equal(parseFloat(expectedCores)))
				Expect(providerConfig.Memory).Should(Equal(parseFloat(expectedMemory)))
				Expect(providerConfig.Disk).Should(Equal(parseFloat(expectedDisk)))
				Expect(providerConfig.SystemNamespace).Should(Equal(systemNamespace))
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
	})

	Describe("SshUser", func() {
		It("should return root", func() {
			Expect(IBMPowerDynamicConfig{}.SshUser()).Should(Equal("root"))
		})
	})

	Describe("CloudProvider methods", func() {
		var (
			mock *mockPowerClient
			cfg  IBMPowerDynamicConfig
		)

		BeforeEach(func() {
			mock = &mockPowerClient{}
			cfg = IBMPowerDynamicConfig{
				pClient:  mock,
				pingFunc: func(_ context.Context, _ string) error { return nil },
			}
		})

		Describe("LaunchInstance", func() {
			When("the Power API returns an instance", func() {
				It("should return the instance ID", func(ctx SpecContext) {
					mock.LaunchInstanceOutput = cloud.InstanceIdentifier("pvm-abc123")

					id, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).ShouldNot(HaveOccurred())
					Expect(string(id)).Should(Equal("pvm-abc123"))
				})
			})

			When("the Power API returns an error", func() {
				It("should return a descriptive error", func(ctx SpecContext) {
					mock.LaunchInstanceErr = errors.New("power launch failed")

					_, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("failed to create a Power Systems instance")))
				})
			})

			When("the TaskRun ID is invalid", func() {
				It("should return a validation error", func(ctx SpecContext) {
					_, err := cfg.LaunchInstance(nil, ctx, "invalid-no-colon", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("invalid TaskRun ID")))
				})
			})
		})

		Describe("CountInstances", func() {
			DescribeTable("instance counting by name prefix",
				func(ctx SpecContext, instanceTag string, instances []*models.PVMInstanceReference, expectedCount int) {
					mock.ListInstancesOutput = models.PVMInstances{PvmInstances: instances}

					count, err := cfg.CountInstances(nil, ctx, instanceTag)

					Expect(err).ShouldNot(HaveOccurred())
					Expect(count).Should(Equal(expectedCount))
				},
				Entry("single matching instance = counted",
					"my-tag",
					[]*models.PVMInstanceReference{
						{ServerName: ptr("my-tag-abc123x"), PvmInstanceID: ptr("id-1")},
					}, 1,
				),
				Entry("matching + non-matching instances = only matching counted",
					"prod-tag",
					[]*models.PVMInstanceReference{
						{ServerName: ptr("prod-tag-abc123x"), PvmInstanceID: ptr("id-1")},
						{ServerName: ptr("other-tag-def456x"), PvmInstanceID: ptr("id-2")},
					}, 1,
				),
				Entry("no matching instances = zero count",
					"my-tag",
					[]*models.PVMInstanceReference{
						{ServerName: ptr("other-tag-abc123x"), PvmInstanceID: ptr("id-1")},
					}, 0,
				),
				Entry("multiple matching instances = all counted",
					"multi",
					[]*models.PVMInstanceReference{
						{ServerName: ptr("multi-tag-abc123x"), PvmInstanceID: ptr("id-1")},
						{ServerName: ptr("multi-tag-def456x"), PvmInstanceID: ptr("id-2")},
					}, 2,
				),
				Entry("empty instance list = zero count",
					"my-tag",
					[]*models.PVMInstanceReference{}, 0,
				),
			)

			When("the Power API returns an error", func() {
				It("should return -1 and a descriptive error", func(ctx SpecContext) {
					mock.ListInstancesErr = errors.New("api failure")

					count, err := cfg.CountInstances(nil, ctx, "tag")

					Expect(err).Should(MatchError(ContainSubstring("failed to fetch Power Systems instances")))
					Expect(count).Should(Equal(-1))
				})
			})
		})

		Describe("GetInstanceAddress", func() {
			When("the instance has a reachable IP", func() {
				It("should return the IP address", func(ctx SpecContext) {
					mock.GetInstanceOutput = &models.PVMInstance{
						PvmInstanceID: ptr("pvm-123"),
						Networks: []*models.PVMInstanceNetwork{
							{ExternalIP: "1.2.3.4"},
						},
					}

					addr, err := cfg.GetInstanceAddress(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(Equal("1.2.3.4"))
				})
			})

			When("the instance has only an internal IP", func() {
				It("should fall back to the internal IP", func(ctx SpecContext) {
					mock.GetInstanceOutput = &models.PVMInstance{
						PvmInstanceID: ptr("pvm-123"),
						Networks: []*models.PVMInstanceNetwork{
							{IPAddress: "10.0.0.5"},
						},
					}

					addr, err := cfg.GetInstanceAddress(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(Equal("10.0.0.5"))
				})
			})

			When("getInstance returns an error", func() {
				It("should return empty string without error (transient)", func(ctx SpecContext) {
					mock.GetInstanceErr = errors.New("api error")

					addr, err := cfg.GetInstanceAddress(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(BeEmpty())
				})
			})

			When("the instance has no networks", func() {
				It("should return empty string without error (transient)", func(ctx SpecContext) {
					mock.GetInstanceOutput = &models.PVMInstance{
						PvmInstanceID: ptr("pvm-123"),
						Networks:      []*models.PVMInstanceNetwork{},
					}

					addr, err := cfg.GetInstanceAddress(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(BeEmpty())
				})
			})

			When("the IP is not live", func() {
				It("should return empty string without error", func(ctx SpecContext) {
					cfg.pingFunc = func(_ context.Context, _ string) error {
						return errors.New("connection refused")
					}
					mock.GetInstanceOutput = &models.PVMInstance{
						PvmInstanceID: ptr("pvm-123"),
						Networks: []*models.PVMInstanceNetwork{
							{ExternalIP: "1.2.3.4"},
						},
					}

					addr, err := cfg.GetInstanceAddress(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(BeEmpty())
				})
			})
		})

		Describe("GetState", func() {
			When("the instance is running normally", func() {
				It("should return OKState", func(ctx SpecContext) {
					mock.GetInstanceOutput = &models.PVMInstance{
						Status: ptr("ACTIVE"),
						Health: &models.PVMInstanceHealth{Status: "OK"},
					}

					state, err := cfg.GetState(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(Equal(cloud.OKState))
				})
			})

			When("the instance is in ERROR state with CRITICAL health", func() {
				It("should return FailedState", func(ctx SpecContext) {
					mock.GetInstanceOutput = &models.PVMInstance{
						Status: ptr("ERROR"),
						Health: &models.PVMInstanceHealth{Status: "CRITICAL"},
					}

					state, err := cfg.GetState(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(Equal(cloud.FailedState))
				})
			})

			When("the instance has ERROR status but non-CRITICAL health", func() {
				It("should return OKState", func(ctx SpecContext) {
					mock.GetInstanceOutput = &models.PVMInstance{
						Status: ptr("ERROR"),
						Health: &models.PVMInstanceHealth{Status: "WARNING"},
					}

					state, err := cfg.GetState(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(Equal(cloud.OKState))
				})
			})

			When("getInstance returns an error", func() {
				It("should return empty state without error (transient)", func(ctx SpecContext) {
					mock.GetInstanceErr = errors.New("api error")

					state, err := cfg.GetState(nil, ctx, "pvm-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(BeEmpty())
				})
			})
		})

		Describe("ListInstances", func() {
			When("matching instances with reachable IPs exist", func() {
				It("should return only matching reachable instances", func(ctx SpecContext) {
					mock.ListInstancesOutput = models.PVMInstances{
						PvmInstances: []*models.PVMInstanceReference{
							{
								ServerName:    ptr("my-tag-abc123x"),
								PvmInstanceID: ptr("id-1"),
								Networks:      []*models.PVMInstanceNetwork{{ExternalIP: "1.2.3.4"}},
							},
							{
								ServerName:    ptr("other-tag-def456x"),
								PvmInstanceID: ptr("id-2"),
								Networks:      []*models.PVMInstanceNetwork{{ExternalIP: "5.6.7.8"}},
							},
						},
					}

					instances, err := cfg.ListInstances(nil, ctx, "my-tag")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(instances).Should(HaveLen(1))
					Expect(string(instances[0].InstanceId)).Should(Equal("id-1"))
					Expect(instances[0].Address).Should(Equal("1.2.3.4"))
				})
			})

			When("matching instances have unreachable IPs", func() {
				It("should exclude them from the result", func(ctx SpecContext) {
					cfg.pingFunc = func(_ context.Context, _ string) error {
						return errors.New("connection refused")
					}
					mock.ListInstancesOutput = models.PVMInstances{
						PvmInstances: []*models.PVMInstanceReference{
							{
								ServerName:    ptr("my-tag-abc123x"),
								PvmInstanceID: ptr("id-1"),
								Networks:      []*models.PVMInstanceNetwork{{ExternalIP: "1.2.3.4"}},
							},
						},
					}

					instances, err := cfg.ListInstances(nil, ctx, "my-tag")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(instances).Should(BeEmpty())
				})
			})

			When("matching instances have no networks", func() {
				It("should exclude them from the result", func(ctx SpecContext) {
					mock.ListInstancesOutput = models.PVMInstances{
						PvmInstances: []*models.PVMInstanceReference{
							{
								ServerName:    ptr("my-tag-abc123x"),
								PvmInstanceID: ptr("id-1"),
								Networks:      []*models.PVMInstanceNetwork{},
							},
						},
					}

					instances, err := cfg.ListInstances(nil, ctx, "my-tag")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(instances).Should(BeEmpty())
				})
			})

			When("the Power API returns an error", func() {
				It("should return the error", func(ctx SpecContext) {
					mock.ListInstancesErr = errors.New("api failure")

					_, err := cfg.ListInstances(nil, ctx, "tag")

					Expect(err).Should(MatchError(ContainSubstring("failed to fetch Power Systems instances")))
				})
			})

			When("no instances match the tag", func() {
				It("should return an empty list", func(ctx SpecContext) {
					mock.ListInstancesOutput = models.PVMInstances{
						PvmInstances: []*models.PVMInstanceReference{
							{
								ServerName:    ptr("other-abc123x"),
								PvmInstanceID: ptr("id-1"),
								Networks:      []*models.PVMInstanceNetwork{{ExternalIP: "1.2.3.4"}},
							},
						},
					}

					instances, err := cfg.ListInstances(nil, ctx, "my-tag")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(instances).Should(BeEmpty())
				})
			})
		})

		Describe("TerminateInstance", func() {
			When("the Power API succeeds", func() {
				It("should return nil", func(ctx SpecContext) {
					Expect(cfg.TerminateInstance(nil, ctx, "pvm-123")).ShouldNot(HaveOccurred())
				})
			})

			When("the initial delete fails", func() {
				It("should still return nil (error is swallowed, retry loop continues)", func(ctx SpecContext) {
					mock.DeleteInstanceErr = errors.New("delete failed")

					Expect(cfg.TerminateInstance(nil, ctx, "pvm-123")).ShouldNot(HaveOccurred())
				})
			})
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
