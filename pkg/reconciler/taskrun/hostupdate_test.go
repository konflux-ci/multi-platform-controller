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

func hostDataFromTRSpec(specParams v1.Params) map[string]string {
	newHostData := make(map[string]string)

	for _, specParam := range specParams {
		switch key := specParam.Name; key {
		case "address":
			newHostData["address"] = specParam.Value.StringVal
		case "secret":
			newHostData["secret"] = specParam.Value.StringVal
		case "concurrency":
			newHostData["concurrency"] = specParam.Value.StringVal
		case "user":
			newHostData["user"] = specParam.Value.StringVal
		case "platform":
			newHostData["platform"] = specParam.Value.StringVal
		default:
			// Not really needed
		}
	}

	return newHostData
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
		func(hostConfigData map[string]string, shouldFail bool) {

			hostConfig.Data = hostConfigData

			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(hostConfig).Build()
			log := logr.FromContextOrDiscard(context.TODO())

			// tested function call
			UpdateHostPools(testNamespace, k8sClient, &log)

			// get list of all TaskRuns, as we cannot predict the name
			createdList := v1.TaskRunList{}

			// test everything in TaskRun creation that is not part of the table testing
			Eventually(func(g Gomega) {
				// TaskRun successfully created
				g.Expect(k8sClient.List(context.TODO(), &createdList, client.InNamespace(testNamespace))).To(Succeed())
			}).Should(Succeed())

			// Only one TaskRun was created == hostConfigData was good data
			Expect(len(createdList.Items) == 0).To(Equal(shouldFail))

			if !shouldFail {
				// set label field filled correctly
				Expect(createdList.Items[0].Labels).To(HaveKeyWithValue(TaskTypeLabel, TaskTypeUpdate))

				// extract TaskRun data to begin testing individual fields were correctly filled
				updatedHostData := hostDataFromTRSpec(createdList.Items[0].Spec.Params)

				// validate each field is exactly as it's expected to be
				Expect(updatedHostData["address"]).To(Equal(hostConfigData["address"]))
				Expect(updatedHostData["secret"]).To(Equal(hostConfigData["secret"]))
				Expect(updatedHostData["concurrency"]).To(Equal(hostConfigData["concurrency"]))
				Expect(updatedHostData["user"]).To(Equal(hostConfigData["user"]))
				Expect(updatedHostData["platform"]).To(Equal(hostConfigData["platform"]))
			}
		},
		Entry("Positive test", map[string]string{
			"host.koko-hazamar-prod-1.address":     "10.130.75.23",
			"host.koko-hazamar-prod-1.secret":      "internal-prod-ibm-ssh-key",
			"host.koko-hazamar-prod-1.concurrency": "1",
			"host.koko-hazamar-prod-1.user":        "koko_hazamar",
			"host.koko-hazamar-prod-1.platform":    "linux/ppc64le"}, false),
		Entry("Negative test - empty data map", map[string]string{}, true),
		Entry("Negative test - dynamic host keys", map[string]string{
			"dynamic.moshe-kipod-prod-1.address":     "10.130.75.23",
			"dynamic.moshe-kipod-prod-1.secret":      "internal-prod-ibm-ssh-key",
			"dynamic.moshe-kipod-prod-1.concurrency": "1",
			"dynamic.moshe-kipod-prod-1.user":        "koko_hazamar",
			"dynamic.moshe-kipod-prod-1.platform":    "linux/ppc64le"}, true),
		Entry("Negative test - bad key format", map[string]string{
			"host.address":     "10.130.75.23",
			"host.secret":      "internal-prod-ibm-ssh-key",
			"host.concurrency": "1",
			"host.user":        "root",
			"host.platform":    "linux/ppc64le"}, true),
		Entry("Negative test - bad address field", map[string]string{
			"host.koko-hazamar-prod-1.address":     "10.130",
			"host.koko-hazamar-prod-1.secret":      "internal-prod-ibm-ssh-key",
			"host.koko-hazamar-prod-1.concurrency": "1",
			"host.koko-hazamar-prod-1.user":        "koko_hazamar",
			"host.koko-hazamar-prod-1.platform":    "linux/ppc64le"}, true),
		Entry("Negative test - bad secret field", map[string]string{
			"host.koko-hazamar-prod-1.address":     "10.130.75.23",
			"host.koko-hazamar-prod-1.secret":      "",
			"host.koko-hazamar-prod-1.concurrency": "1",
			"host.koko-hazamar-prod-1.user":        "koko_hazamar",
			"host.koko-hazamar-prod-1.platform":    "linux/ppc64le"}, true),
		Entry("Negative test - bad concurrency part I", map[string]string{
			"host.koko-hazamar-prod-1.address":     "10.130.75.23",
			"host.koko-hazamar-prod-1.secret":      "internal-prod-ibm-ssh-key",
			"host.koko-hazamar-prod-1.concurrency": "-1",
			"host.user":                            "koko_hazamar",
			"host.koko-hazamar-prod-1.platform":    "linux/ppc64le"}, true),
		Entry("Negative test - bad concurrency part II", map[string]string{
			"host.koko-hazamar-prod-1.address":     "10.130.75.23",
			"host.koko-hazamar-prod-1.secret":      "internal-prod-ibm-ssh-key",
			"host.koko-hazamar-prod-1.concurrency": "1234567890",
			"host.user":                            "koko_hazamar",
			"host.koko-hazamar-prod-1.platform":    "linux/ppc64le"}, true),
		Entry("Negative test - bad user", map[string]string{
			"host.koko-hazamar-prod-1.address":     "10.130.75.23",
			"host.koko-hazamar-prod-1.secret":      "internal-prod-ibm-ssh-key",
			"host.koko-hazamar-prod-1.concurrency": "1",
			"host.koko-hazamar-prod-1.user":        "root",
			"host.koko-hazamar-prod-1.platform":    "linux/ppc64le"}, true),
		Entry("Negative test - bad platform", map[string]string{
			"host.koko-hazamar-prod-1.address":     "10.130.75.23",
			"host.koko-hazamar-prod-1.secret":      "internal-prod-ibm-ssh-key",
			"host.koko-hazamar-prod-1.concurrency": "1",
			"host.koko-hazamar-prod-1.user":        "koko_hazamar",
			"host.koko-hazamar-prod-1.platform":    "linux/moshe_kipod555"}, true),
	)
})
