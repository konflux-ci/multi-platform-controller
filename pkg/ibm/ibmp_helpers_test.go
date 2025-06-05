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
			func(userTags models.Tags, existingTaskRuns map[string][]string, expectedResult bool) {
				ibmp := IBMPowerDynamicConfig{}
				instance.UserTags = userTags
				Expect(ibmp.doesInstanceHaveTaskRun(logr.Discard(), instance, existingTaskRuns)).
					Should(Equal(expectedResult))
			},
			Entry("no user tags",
				models.Tags{},
				map[string][]string{}, false),
			Entry("no existing TaskRuns",
				models.Tags{"test-namespace:test-task"},
				map[string][]string{}, false),
			Entry("no valid TaskRun ID",
				models.Tags{"a", "b", "c"},
				map[string][]string{}, false),
			Entry("non-existing TaskRun ID",
				models.Tags{"test-namespace:test-task"},
				map[string][]string{"namespace": {"task"}}, false),
			Entry("existing TaskRun ID",
				models.Tags{"test-namespace:test-task"},
				map[string][]string{"test-namespace": {"test-task"}},
				true,
			),
		)
	})

	// A unit test for retrieveInstanceIp. For now only tests the logic paths for retrieving an IP address from
	// PVMInstanceNetwork's ExternalIP or IPAddress, as it is currently written:
	// 	1. Verifying that a VM's first network is the one chosen to check for IP addresses.
	// 	2. Verifying that is an ExternalIP exists, it is the one returned.
	// 	3. Verifying that if an ExternalIP does not exist but an IPAddress does, the IPAddress is the one returned.
	// 	4. Verifying that if the slice of models.PVMInstanceNetwork retrieveInstanceIP gets does not contain any networks
	//     or if the first network in the slice has no ExternalIP or IPAddress, the correct return behavior occurs
	//     (including the error message containing the correct reason for the error)
	Describe("retrieveInstanceIp helper function tests", func() {

		When("an IP address should be found", func() {
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
					Expect(retrieveInstanceIp(instanceID, networks)).
						Should(Equal(expectedIP))
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
			var mockNetworkWithNoIPs = &models.PVMInstanceNetwork{
				Href:        "https://cloud.ibm.com/v1/moshe_kipod",
				NetworkName: "koko_hazamar",
			}

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
	})
})
