// Testing UpdateHostPools - that runs the host update task periodically for static host pools.
// The spec checks that:
//	- That one test TaskRun has been created when it should and that none were created when the configuration data is incorrect, that the TaskRun
//	- That the TaskRun created was a host updating TaskRun was created
//	- That the configuration data in the TaskRun spec Params and Workspace contain the test data
//
// There are 9 test cases:
// 	1. A positive test to verify all is working correctly
//	2. A negative test with no configuration data
//	3. A negative test to verify UpdateHostPools only creates TaskRuns for static hosts
//	4. A negative test to verify UpdateHostPools only creates TaskRuns when the spec Param key has the correct syntax
//	5. A negative test to verify data validation on the host address field
//	6. A negative test to verify data validation on the host concurrency field
//	7. Another negative test to data verify on the host concurrency field
//	8. A negative test to verify data validation on the host username field
//	9. A negative test to verify data validation on the host platform field

package taskrun

import (
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "default"

// hostDataFromTRSpec creates a map[string]string of configuration data that can be compared
// to the test case data, from the TaskRun input
func hostDataFromTRSpec(updateTR v1.TaskRun) map[string]string {
	newHostData := make(map[string]string)

	specParams := updateTR.Spec.Params
	for _, specParam := range specParams {
		switch key := specParam.Name; key {

		case "HOST":
			newHostData["address"] = specParam.Value.StringVal
		case "USER":
			newHostData["user"] = specParam.Value.StringVal
		case "CONCURRENCY":
			newHostData["concurrency"] = specParam.Value.StringVal
		case "PLATFORM":
			newHostData["platform"] = specParam.Value.StringVal
		default:
			// Not really needed
		}
	}

	newHostData["secret"] = updateTR.Spec.Workspaces[0].Secret.SecretName

	return newHostData
}

// testConfigDataFromTestData adds a suffix to the test data to create a key format for the TaskRun Spec Params
// that UpdateHostPools recognizes as having the correct syntax
func testConfigDataFromTestData(testData map[string]string, configKeySuffix string) map[string]string {
	testConfigData := make(map[string]string)

	for k, v := range testData {
		suffixedKey := configKeySuffix + k
		testConfigData[suffixedKey] = v
	}

	return testConfigData
}

// HostUpdateTaskRunTest - Ginkgo table testing spec for HostUpdateTaskRunTest. Creates a new ConfigMap for each
// test case and runs them separately
var _ = Describe("HostUpdateTaskRunTest", func() {
	var scheme = runtime.NewScheme()
	var hostConfig = &corev1.ConfigMap{}

	BeforeEach(func() {
		utilruntime.Must(corev1.AddToScheme(scheme))
		utilruntime.Must(v1.AddToScheme(scheme))

		hostConfig = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      HostConfig,
				Namespace: testNamespace,
			},
			Data: map[string]string{"test data": "will replace this"},
		}
	})

	DescribeTable("Creating taskruns for updating static hosts in a pool",
		func(ctx SpecContext, hostConfigData map[string]string, hostSuffix string, shouldFail bool) {

			hostConfig.Data = testConfigDataFromTestData(hostConfigData, hostSuffix)

			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(hostConfig).Build()
			log := logr.FromContextOrDiscard(ctx)
			zeroTaskRuns := false

			// tested function call
			UpdateHostPools(testNamespace, k8sClient, &log)

			// get list of all TaskRuns, as we cannot predict the name
			createdList := v1.TaskRunList{}

			// test everything in TaskRun creation that is not part of the table testing
			Eventually(func(g Gomega) {
				// TaskRun successfully created
				g.Expect(k8sClient.List(ctx, &createdList, client.InNamespace(testNamespace))).To(Succeed())

				// Only one TaskRun was created == hostConfigData was good data
				zeroTaskRuns = len(createdList.Items) == 0
				g.Expect(zeroTaskRuns).To(Equal(shouldFail))

			}).Should(Succeed())

			if !zeroTaskRuns {
				// set label field filled correctly
				Expect(createdList.Items[0].Labels).To(HaveKeyWithValue(TaskTypeLabel, TaskTypeUpdate))

				// extract TaskRun data to begin testing individual fields were correctly filled
				updatedHostData := hostDataFromTRSpec(createdList.Items[0])

				// validate each field is exactly as it's expected to be
				Expect(hostConfigData["address"]).To(Equal(updatedHostData["address"]))
				Expect(hostConfigData["user"]).To(Equal(updatedHostData["user"]))
				Expect(updatedHostData["secret"]).To(Equal(updatedHostData["secret"]))
				Expect(hostConfigData["concurrency"]).To(Equal(updatedHostData["concurrency"]))
				Expect(hostConfigData["platform"]).To(Equal(updatedHostData["platform"]))
			}
		},
		Entry("Positive test", map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-koko-hazamar-ssh-key",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le"},
			"host.koko-hazamar-prod-1.", false),
		Entry("Negative test - empty data map", map[string]string{}, "", true),
		Entry("Negative test - dynamic host keys", map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-moshe-kipod-ssh-key",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le"},
			"dynamic.moshe-kipod-prod-1.", true),
		Entry("Negative test - bad key format", map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-prod-ibm-ssh-key",
			"concurrency": "1",
			"user":        "root",
			"platform":    "linux/ppc64le"},
			"host.", true),
		Entry("Negative test - bad address field", map[string]string{
			"address":     "10.130",
			"secret":      "internal-prod-ibm-ssh-key",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le"},
			"host.koko-hazamar-prod-1.", true),
		Entry("Negative test - bad secret field", map[string]string{
			"address":     "10.130.75.23",
			"secret":      "",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le"},
			"host.koko-hazamar-prod-1.", true),
		Entry("Negative test - bad concurrency part I", map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-prod-ibm-ssh-key",
			"concurrency": "-1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le"},
			"host.koko-hazamar-prod-1.", true),
		Entry("Negative test - bad concurrency part II", map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-prod-ibm-ssh-key",
			"concurrency": "1234567890",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le"},
			"host.koko-hazamar-prod-1.", true),
		Entry("Negative test - bad user", map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-prod-ibm-ssh-key",
			"concurrency": "1",
			"user":        "root",
			"platform":    "linux/ppc64le"},
			"host.koko-hazamar-prod-1.", true),
		Entry("Negative test - bad platform", map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-prod-ibm-ssh-key",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/moshe_kipod555"},
			"host.koko-hazamar-prod-1.", true),
	)
})
