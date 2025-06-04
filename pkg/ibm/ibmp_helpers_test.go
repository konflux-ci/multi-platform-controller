package ibm

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/go-logr/logr"
)

var _ = Describe("IBM Power Cloud Helper Functions", func() {

	Describe("doesInstanceHaveTaskRun helper function tests", func() {
		var instance *models.PVMInstance

		BeforeEach(func() {
			instance = &models.PVMInstance{
				PvmInstanceID: ptr("id"),
			}
		})
		DescribeTable("Determine if a VM instance is linked to a non-existing TaskRun",
			func(log logr.Logger, userTags models.Tags, existingTaskRuns map[string][]string, expectedResult bool) {
				ibmp := IBMPowerDynamicConfig{}
				instance.UserTags = userTags
				Expect(ibmp.doesInstanceHaveTaskRun(log, instance, existingTaskRuns)).Should(Equal(expectedResult))
			},
			Entry("no user tags", logr.Discard(),
				models.Tags{},
				map[string][]string{}, false),
			Entry("no existing TaskRuns", logr.Discard(),
				models.Tags{"test-namespace:test-task"},
				map[string][]string{}, false),
			Entry("no valid TaskRun ID", logr.Discard(),
				models.Tags{"a", "b", "c"},
				map[string][]string{}, false),
			Entry("non-existing TaskRun ID", logr.Discard(),
				models.Tags{"test-namespace:test-task"},
				map[string][]string{"namespace": {"task"}}, false),
			Entry("existing TaskRun ID", logr.Discard(),
				models.Tags{"test-namespace:test-task"},
				map[string][]string{"test-namespace": {"test-task"}},
				true,
			),
		)
	})

	Describe("retrieveInstanceIp helper function tests", func() {

		When("an IP should be found", func() {

			var (
				mockNetworkWithExternalIP = &models.PVMInstanceNetwork{
					ExternalIP: "1.2.3.4",
					IPAddress:  "10.0.0.1",
				}
				mockNetworkWithOnlyInternalIP = &models.PVMInstanceNetwork{
					IPAddress: "10.0.0.2",
				}
			)

			DescribeTable("it returns the correct IP address",
				func(instanceID string, networks []*models.PVMInstanceNetwork, expectedIP string) {
					ip, err := retrieveInstanceIp(instanceID, networks)
					Expect(err).Should(BeNil())
					Expect(ip).Should(Equal(expectedIP))
				},
				Entry("when an external IP exists, it is preferred",
					"vm-with-external-ip",
					[]*models.PVMInstanceNetwork{mockNetworkWithExternalIP},
					"1.2.3.4",
				),
				Entry("when only an internal IP exists, it is used as a fallback",
					"vm-with-internal-ip",
					[]*models.PVMInstanceNetwork{mockNetworkWithOnlyInternalIP},
					"10.0.0.2",
				),
				Entry("when multiple networks exist, it only uses the first one",
					"vm-with-multiple-networks-with-ips",
					[]*models.PVMInstanceNetwork{mockNetworkWithExternalIP, mockNetworkWithOnlyInternalIP},
					"1.2.3.4",
				),
			)
		})

		When("an IP is missing and an error is expected", func() {

			var mockNetworkWithNoIPs = &models.PVMInstanceNetwork{}

			DescribeTable("it returns an accurately descriptive error",
				func(instanceID string, networks []*models.PVMInstanceNetwork, expectedErrorSubstring string) {
					ip, err := retrieveInstanceIp(instanceID, networks)
					Expect(ip).Should(BeEmpty())
					Expect(err).Should(HaveOccurred())
					Expect(err.Error()).Should(ContainSubstring(expectedErrorSubstring))
				},
				Entry("when the network slice is empty",
					"vm-with-no-ip",
					[]*models.PVMInstanceNetwork{},
					"no networks found",
				),
				Entry("when the network slice is nil",
					"vm-with-no-ip",
					nil,
					"no networks found",
				),
				Entry("when the network slice has no IP fields",
					"vm-with-no-ip",
					[]*models.PVMInstanceNetwork{mockNetworkWithNoIPs},
					"no IP address found",
				),
			)
		})

		When("the IP address format is invalid", func() {
			var (
				garbageIP            = "not-a-valid-ip-address"
				networkWithGarbageIP = &models.PVMInstanceNetwork{
					ExternalIP: garbageIP,
				}
			)

			It("should ideally return an error but currently returns the malformed string", func() {
				ip, err := retrieveInstanceIp("vm-with-garbage-ip", []*models.PVMInstanceNetwork{networkWithGarbageIP})
				// when bug is fixed, this will be deleted
				GinkgoWriter.Printf("'%s' is not a valid IP, but retrieveInstanceIp returned it as ip: '%s'", garbageIP, ip)
				// and only this will exist
				Expect(err).Should(HaveOccurred())
				Expect(ip).Should(BeEmpty())
				Expect(err.Error()).Should(ContainSubstring("invalid IP address format"))
			})
		})

		When("the network slice contains a nil entry (demonstrating a bug)", func() {
			It("should not panic", func() {
				Expect(func() {
					retrieveInstanceIp("vm-with-nil-network", []*models.PVMInstanceNetwork{nil})
				}).ShouldNot(Panic())
			})
		})
	})
})
