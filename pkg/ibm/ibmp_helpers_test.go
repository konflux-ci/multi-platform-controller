package ibm

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/go-logr/logr"
)

var _ = Describe("IBM Power Cloud Helper Functions", func() {
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
			Expect(ibmp.doesInstanceHaveTaskRun(log, instance, existingTaskRuns)).To(Equal(expectedResult))
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
