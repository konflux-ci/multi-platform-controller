package taskrun

import (
	"context"

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

func testConfigDataFromTestData(testData map[string]string, configKeySuffix string) map[string]string {
	testConfigData := make(map[string]string)

	for k, v := range testData {
		suffixedKey := configKeySuffix + k
		testConfigData[suffixedKey] = v
	}

	return testConfigData
}

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
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: "v1",
			},
			Data: map[string]string{"test data": "will replace this"},
		}
	})

	DescribeTable("Creating taskruns for updating static hosts in a pool",
		func(hostConfigData map[string]string, hostSuffix string, shouldFail bool) {

			hostConfig.Data = testConfigDataFromTestData(hostConfigData, hostSuffix)

			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(hostConfig).Build()
			log := logr.FromContextOrDiscard(context.TODO())
			zeroTaskRuns := false

			// tested function call
			UpdateHostPools(testNamespace, k8sClient, &log)

			// get list of all TaskRuns, as we cannot predict the name
			createdList := v1.TaskRunList{}

			// test everything in TaskRun creation that is not part of the table testing
			Eventually(func(g Gomega) {
				// TaskRun successfully created
				g.Expect(k8sClient.List(context.TODO(), &createdList, client.InNamespace(testNamespace))).To(Succeed())

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
