package ibm

import (
	"context"
	"errors"
	"time"

	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-openapi/strfmt"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newTestVpc returns a minimal VPC fixture for tests that need lookUpVpc to succeed.
func newTestVpc() *vpcv1.VPCCollection {
	return &vpcv1.VPCCollection{
		Vpcs: []vpcv1.VPC{{
			ID:   ptr("vpc-id-1"),
			Name: ptr("test-vpc"),
			ResourceGroup: &vpcv1.ResourceGroupReference{
				ID: ptr("rg-id-1"),
			},
			DefaultSecurityGroup: &vpcv1.SecurityGroupReference{
				ID: ptr("sg-id-1"),
			},
		}},
	}
}

var _ = Describe("IBM System Z Unit Tests", func() {

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
	Describe("CreateIbmZCloudConfig", func() {
		DescribeTable("config parsing",
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

				Expect(providerConfig.Region).Should(Equal("test-region"))
				Expect(providerConfig.Key).Should(Equal("test-key"))
				Expect(providerConfig.Subnet).Should(Equal("test-subnet"))
				Expect(providerConfig.Vpc).Should(Equal("test-vpc"))
				Expect(providerConfig.ImageId).Should(Equal("test-image-id"))
				Expect(providerConfig.Secret).Should(Equal("test-secret"))
				Expect(providerConfig.Url).Should(Equal("test-url"))
				Expect(providerConfig.Profile).Should(Equal("test-profile"))
				Expect(providerConfig.PrivateIP).Should(Equal(testConfig["private-ip"] == "true"))
				Expect(providerConfig.Disk).Should(Equal(expectedDisk))
				Expect(providerConfig.SystemNamespace).Should(Equal(systemNamespace))
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
	})

	Describe("SshUser", func() {
		It("should return the default SSH user", func() {
			Expect(IBMZDynamicConfig{}.SshUser()).Should(Equal(defaultSSHUser))
		})
	})

	Describe("getVpcClient", func() {
		When("vClient is a typed-nil pointer", func() {
			It("should return an error instead of panicking", func(ctx SpecContext) {
				var nilClient *mockVpcClient
				cfg := IBMZDynamicConfig{vClient: nilClient}

				_, err := cfg.getVpcClient(ctx, nil)

				Expect(err).Should(MatchError(ContainSubstring("typed-nil")))
			})
		})
	})

	Describe("CloudProvider methods", func() {
		var (
			mock *mockVpcClient
			cfg  IBMZDynamicConfig
		)

		BeforeEach(func() {
			mock = &mockVpcClient{}
			mock.ListVpcsOutput = newTestVpc()
			cfg = IBMZDynamicConfig{
				Vpc:       "test-vpc",
				Key:       "test-key",
				Subnet:    "test-subnet",
				Region:    "us-east-2",
				Profile:   "bz2-1x4",
				ImageId:   "img-123",
				Disk:      100,
				PrivateIP: true,
				vClient:   mock,
				pingFunc:  func(_ context.Context, _ string) error { return nil },
			}
		})

		Describe("LaunchInstance", func() {
			BeforeEach(func() {
				mock.ListKeysOutput = &vpcv1.KeyCollection{
					Keys: []vpcv1.Key{{ID: ptr("key-id-1"), Name: ptr("test-key")}},
				}
				mock.ListSubnetsOutput = &vpcv1.SubnetCollection{
					Subnets: []vpcv1.Subnet{{ID: ptr("subnet-id-1"), Name: ptr("test-subnet")}},
				}
			})

			When("the VPC API returns an instance", func() {
				It("should return the instance ID", func(ctx SpecContext) {
					mock.CreateInstanceOutput = &vpcv1.Instance{ID: ptr("z-inst-abc123")}

					id, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).ShouldNot(HaveOccurred())
					Expect(string(id)).Should(Equal("z-inst-abc123"))
				})
			})

			When("the VPC API returns an error on CreateInstance", func() {
				It("should return a descriptive error", func(ctx SpecContext) {
					mock.CreateInstanceErr = errors.New("vpc create failed")

					_, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("failed to create the System Z virtual server instance")))
				})
			})

			When("the TaskRun ID is invalid", func() {
				It("should return a validation error", func(ctx SpecContext) {
					_, err := cfg.LaunchInstance(nil, ctx, "invalid-no-colon", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("invalid TaskRun ID")))
				})
			})

			When("the VPC lookup fails", func() {
				It("should return the lookup error", func(ctx SpecContext) {
					mock.ListVpcsErr = errors.New("vpc list failed")

					_, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("failed to list VPC networks")))
				})
			})

			When("the SSH key is not found", func() {
				It("should return a not-found error", func(ctx SpecContext) {
					mock.ListKeysOutput = &vpcv1.KeyCollection{Keys: []vpcv1.Key{}}

					_, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("failed to find SSH key")))
				})
			})

			When("the subnet is not found", func() {
				It("should return a not-found error", func(ctx SpecContext) {
					mock.ListSubnetsOutput = &vpcv1.SubnetCollection{Subnets: []vpcv1.Subnet{}}

					_, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("failed to find subnet")))
				})
			})
		})

		Describe("CountInstances", func() {
			DescribeTable("instance counting by name prefix",
				func(ctx SpecContext, instanceTag string, instances []vpcv1.Instance, expectedCount int) {
					mock.ListInstancesOutput = &vpcv1.InstanceCollection{Instances: instances}

					count, err := cfg.CountInstances(nil, ctx, instanceTag)

					Expect(err).ShouldNot(HaveOccurred())
					Expect(count).Should(Equal(expectedCount))
				},
				Entry("single matching instance",
					"my-tag",
					[]vpcv1.Instance{{Name: ptr("my-tag-abc123x"), ID: ptr("id-1")}},
					1,
				),
				Entry("matching + non-matching = only matching counted",
					"prod",
					[]vpcv1.Instance{
						{Name: ptr("prod-abc123x"), ID: ptr("id-1")},
						{Name: ptr("other-def456x"), ID: ptr("id-2")},
					},
					1,
				),
				Entry("empty list = zero",
					"my-tag",
					[]vpcv1.Instance{},
					0,
				),
			)

			When("the VPC API returns an error on ListInstances", func() {
				It("should return -1 and the error", func(ctx SpecContext) {
					mock.ListInstancesErr = errors.New("api failure")

					count, err := cfg.CountInstances(nil, ctx, "tag")

					Expect(err).Should(MatchError(ContainSubstring("failed to list virtual server instances")))
					Expect(count).Should(Equal(-1))
				})
			})
		})

		Describe("GetInstanceAddress", func() {
			When("the instance has a private IP and config uses PrivateIP", func() {
				It("should return the private IP", func(ctx SpecContext) {
					mock.GetInstanceOutput = &vpcv1.Instance{
						ID:     ptr("z-inst-1"),
						Status: ptr(vpcv1.InstanceStatusRunningConst),
						NetworkInterfaces: []vpcv1.NetworkInterfaceInstanceContextReference{
							{PrimaryIP: &vpcv1.ReservedIPReference{Address: ptr("10.0.0.5")}},
						},
					}

					addr, err := cfg.GetInstanceAddress(nil, ctx, "z-inst-1")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(Equal("10.0.0.5"))
				})
			})

			When("GetInstance returns an error", func() {
				It("should return empty string without error (transient)", func(ctx SpecContext) {
					mock.GetInstanceErr = errors.New("api error")

					addr, err := cfg.GetInstanceAddress(nil, ctx, "z-inst-1")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(BeEmpty())
				})
			})

			When("the IP is not live", func() {
				It("should return empty string without error", func(ctx SpecContext) {
					cfg.pingFunc = func(_ context.Context, _ string) error {
						return errors.New("connection refused")
					}
					mock.GetInstanceOutput = &vpcv1.Instance{
						ID:     ptr("z-inst-1"),
						Status: ptr(vpcv1.InstanceStatusRunningConst),
						NetworkInterfaces: []vpcv1.NetworkInterfaceInstanceContextReference{
							{PrimaryIP: &vpcv1.ReservedIPReference{Address: ptr("10.0.0.5")}},
						},
					}

					addr, err := cfg.GetInstanceAddress(nil, ctx, "z-inst-1")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(BeEmpty())
				})
			})
		})

		Describe("GetState", func() {
			DescribeTable("lifecycle state mapping",
				func(ctx SpecContext, lifecycleState string, expectedState cloud.VMState) {
					mock.GetInstanceOutput = &vpcv1.Instance{
						LifecycleState: ptr(lifecycleState),
					}

					state, err := cfg.GetState(nil, ctx, "z-inst-1")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(Equal(expectedState))
				},
				Entry("stable = OKState", "stable", cloud.OKState),
				Entry("pending = OKState", "pending", cloud.OKState),
				Entry("deleting = OKState", "deleting", cloud.OKState),
				Entry("suspended = OKState", "suspended", cloud.OKState),
				Entry("updating = OKState", "updating", cloud.OKState),
				Entry("waiting = OKState", "waiting", cloud.OKState),
				Entry("failed = FailedState", "failed", cloud.FailedState),
			)

			When("GetInstance returns an error", func() {
				It("should return empty state without error (transient)", func(ctx SpecContext) {
					mock.GetInstanceErr = errors.New("api error")

					state, err := cfg.GetState(nil, ctx, "z-inst-1")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(BeEmpty())
				})
			})
		})

		Describe("ListInstances", func() {
			When("matching instances with private IPs exist", func() {
				It("should return only matching reachable instances", func(ctx SpecContext) {
					createdAt := strfmt.DateTime(time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC))
					mock.ListInstancesOutput = &vpcv1.InstanceCollection{
						Instances: []vpcv1.Instance{
							{
								ID:     ptr("id-1"),
								Name:   ptr("my-tag-abc123x"),
								Status: ptr(vpcv1.InstanceStatusRunningConst),
								NetworkInterfaces: []vpcv1.NetworkInterfaceInstanceContextReference{
									{PrimaryIP: &vpcv1.ReservedIPReference{Address: ptr("10.0.0.5")}},
								},
								CreatedAt: &createdAt,
							},
							{
								ID:     ptr("id-2"),
								Name:   ptr("other-def456x"),
								Status: ptr(vpcv1.InstanceStatusRunningConst),
								NetworkInterfaces: []vpcv1.NetworkInterfaceInstanceContextReference{
									{PrimaryIP: &vpcv1.ReservedIPReference{Address: ptr("10.0.0.6")}},
								},
								CreatedAt: &createdAt,
							},
						},
					}

					instances, err := cfg.ListInstances(nil, ctx, "my-tag")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(instances).Should(HaveLen(1))
					Expect(string(instances[0].InstanceId)).Should(Equal("id-1"))
					Expect(instances[0].Address).Should(Equal("10.0.0.5"))
				})
			})

			When("the VPC API returns an error on ListInstances", func() {
				It("should return the error", func(ctx SpecContext) {
					mock.ListInstancesErr = errors.New("api failure")

					_, err := cfg.ListInstances(nil, ctx, "tag")

					Expect(err).Should(MatchError(ContainSubstring("failed to list VPC instances")))
				})
			})
		})

		Describe("TerminateInstance", func() {
			When("called", func() {
				It("should return nil (background goroutine handles deletion)", func(ctx SpecContext) {
					mock.GetInstanceOutput = &vpcv1.Instance{
						ID:     ptr("z-inst-1"),
						Status: ptr(vpcv1.InstanceStatusRunningConst),
					}

					Expect(cfg.TerminateInstance(nil, ctx, "z-inst-1")).ShouldNot(HaveOccurred())
				})
			})
		})
	})

	Describe("assignIPToInstance helper", func() {
		var (
			mock *mockVpcClient
			cfg  IBMZDynamicConfig
		)

		BeforeEach(func() {
			mock = &mockVpcClient{}
			cfg = IBMZDynamicConfig{PrivateIP: false, vClient: mock}
		})

		When("PrivateIP is true and the instance has a valid private IP", func() {
			It("should return the private IP without VPC calls", func() {
				cfg.PrivateIP = true
				instance := &vpcv1.Instance{
					ID:     ptr("inst-1"),
					Status: ptr(vpcv1.InstanceStatusRunningConst),
					NetworkInterfaces: []vpcv1.NetworkInterfaceInstanceContextReference{
						{PrimaryIP: &vpcv1.ReservedIPReference{Address: ptr("10.0.0.99")}},
					},
				}

				ip, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(ip).Should(Equal("10.0.0.99"))
			})
		})

		When("PrivateIP is true but the IP is 0.0.0.0", func() {
			It("should return empty string", func() {
				cfg.PrivateIP = true
				instance := &vpcv1.Instance{
					ID:     ptr("inst-1"),
					Status: ptr(vpcv1.InstanceStatusRunningConst),
					NetworkInterfaces: []vpcv1.NetworkInterfaceInstanceContextReference{
						{PrimaryIP: &vpcv1.ReservedIPReference{Address: ptr("0.0.0.0")}},
					},
				}

				ip, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(ip).Should(BeEmpty())
			})
		})

		When("the instance has an existing network interface floating IP", func() {
			It("should return the associated floating IP", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusRunningConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{
					FloatingIps: []vpcv1.FloatingIP{{Address: ptr("52.1.2.3")}},
				}

				ip, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(ip).Should(Equal("52.1.2.3"))
			})
		})

		When("the instance is in a deleting state", func() {
			It("should return an error", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusDeletingConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}

				_, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).Should(MatchError(ContainSubstring("instance was deleted")))
			})
		})

		When("the instance is in a failed state", func() {
			It("should return an error", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusFailedConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}

				_, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).Should(MatchError(ContainSubstring("instance failed")))
			})
		})

		When("the instance is pending", func() {
			It("should return empty string without error", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusPendingConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}

				ip, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(ip).Should(BeEmpty())
			})
		})

		When("an available floating IP exists in the region", func() {
			It("should assign and return it", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusRunningConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
					ResourceGroup:           &vpcv1.ResourceGroupReference{ID: ptr("rg-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}
				mock.ListFloatingIpsOutput = &vpcv1.FloatingIPCollection{
					FloatingIps: []vpcv1.FloatingIP{
						{ID: ptr("fip-1"), Address: ptr("52.10.20.30"), Status: ptr(vpcv1.FloatingIPStatusAvailableConst)},
					},
				}

				ip, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(ip).Should(Equal("52.10.20.30"))
			})
		})

		When("no floating IPs are available and a new one is allocated", func() {
			It("should create, assign, and return the new IP", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusRunningConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
					ResourceGroup:           &vpcv1.ResourceGroupReference{ID: ptr("rg-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}
				mock.ListFloatingIpsOutput = &vpcv1.FloatingIPCollection{}
				mock.CreateFloatingIPOutput = &vpcv1.FloatingIP{
					ID:      ptr("new-fip-1"),
					Address: ptr("52.99.88.77"),
				}

				ip, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(ip).Should(Equal("52.99.88.77"))
			})
		})
		
		
		
		When("ListInstanceNetworkInterfaceFloatingIps returns an error", func() {
			It("should fall through to assignFloatingIP and return an available IP", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusRunningConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
					ResourceGroup:           &vpcv1.ResourceGroupReference{ID: ptr("rg-1")},
				}
				mock.ListNetIfaceFloatingIpsErr = errors.New("network interface lookup failed")
				mock.ListFloatingIpsOutput = &vpcv1.FloatingIPCollection{
					FloatingIps: []vpcv1.FloatingIP{
						{ID: ptr("fip-1"), Address: ptr("52.10.20.30"), Status: ptr(vpcv1.FloatingIPStatusAvailableConst)},
					},
				}

				ip, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(ip).Should(Equal("52.10.20.30"))
			})
		})

		When("the instance has nil PrimaryNetworkInterface", func() {
			It("should gracefully fall through assignNetworkInterfaceFloatingIP without panicking", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusRunningConst),
					PrimaryNetworkInterface: nil,
					ResourceGroup:           &vpcv1.ResourceGroupReference{ID: ptr("rg-1")},
				}
				// No available floating IPs so assignFloatingIP returns ("", nil)
				// without accessing PrimaryNetworkInterface.
				mock.ListFloatingIpsOutput = &vpcv1.FloatingIPCollection{}
				// CreateFloatingIP fails so assignNewlyAllocatedIP returns early
				// without accessing PrimaryNetworkInterface.
				mock.CreateFloatingIPErr = errors.New("quota exceeded")

				_, err := cfg.assignIPToInstance(instance, mock)

				// The nil guard in assignNetworkInterfaceFloatingIP prevents a panic.
				// The function continues through the status switch and eventually
				// returns the CreateFloatingIP error.
				Expect(err).Should(MatchError(ContainSubstring("failed to create a floating IP address")))
			})
		})

		DescribeTable("status-based behavior in assignIPToInstance",
			func(status string, expectedIP string, expectErr bool, errSubstring string) {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(status),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
					ResourceGroup:           &vpcv1.ResourceGroupReference{ID: ptr("rg-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}

				ip, err := cfg.assignIPToInstance(instance, mock)

				if expectErr {
					Expect(err).Should(MatchError(ContainSubstring(errSubstring)))
				} else {
					Expect(err).ShouldNot(HaveOccurred())
				}
				Expect(ip).Should(Equal(expectedIP))
			},
			Entry("stopped returns error",
				vpcv1.InstanceStatusStoppedConst, "", true, "instance was stopped"),
			Entry("stopping returns error",
				vpcv1.InstanceStatusStoppingConst, "", true, "instance was stopping"),
			Entry("restarting returns empty string without error",
				vpcv1.InstanceStatusRestartingConst, "", false, ""),
			Entry("starting returns empty string without error",
				vpcv1.InstanceStatusStartingConst, "", false, ""),
		)

		When("no floating IPs are available and CreateFloatingIP fails", func() {
			It("should return an error about failing to create a floating IP", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusRunningConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
					ResourceGroup:           &vpcv1.ResourceGroupReference{ID: ptr("rg-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}
				mock.ListFloatingIpsOutput = &vpcv1.FloatingIPCollection{}
				mock.CreateFloatingIPErr = errors.New("quota exceeded")

				_, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).Should(MatchError(ContainSubstring("failed to create a floating IP address")))
			})
		})

		When("an available floating IP exists but AddInstanceNetworkInterfaceFloatingIP fails", func() {
			It("should return an error about failing to assign the floating IP", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusRunningConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
					ResourceGroup:           &vpcv1.ResourceGroupReference{ID: ptr("rg-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}
				mock.ListFloatingIpsOutput = &vpcv1.FloatingIPCollection{
					FloatingIps: []vpcv1.FloatingIP{
						{ID: ptr("fip-1"), Address: ptr("52.10.20.30"), Status: ptr(vpcv1.FloatingIPStatusAvailableConst)},
					},
				}
				mock.AddNetIfaceFloatingIPErr = errors.New("permission denied")

				_, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).Should(MatchError(ContainSubstring("failed to assign the floating IP")))
			})
		})

		When("a new floating IP is created but assigning it to the network interface fails", func() {
			It("should return an error about failing to assign the floating IP address", func() {
				instance := &vpcv1.Instance{
					ID:                      ptr("inst-1"),
					Status:                  ptr(vpcv1.InstanceStatusRunningConst),
					PrimaryNetworkInterface: &vpcv1.NetworkInterfaceInstanceContextReference{ID: ptr("nic-1")},
					ResourceGroup:           &vpcv1.ResourceGroupReference{ID: ptr("rg-1")},
				}
				mock.ListNetIfaceFloatingIpsOutput = &vpcv1.FloatingIPUnpaginatedCollection{}
				mock.ListFloatingIpsOutput = &vpcv1.FloatingIPCollection{
					FloatingIps: []vpcv1.FloatingIP{
						{ID: ptr("fip-old"), Address: ptr("52.0.0.1"), Status: ptr("deleting")},
					},
				}
				mock.CreateFloatingIPOutput = &vpcv1.FloatingIP{
					ID:      ptr("new-fip-1"),
					Address: ptr("52.99.88.77"),
				}
				mock.AddNetIfaceFloatingIPErr = errors.New("attach failed")

				_, err := cfg.assignIPToInstance(instance, mock)

				Expect(err).Should(MatchError(ContainSubstring("failed to assign the floating IP address")))
			})
		})
	})
})