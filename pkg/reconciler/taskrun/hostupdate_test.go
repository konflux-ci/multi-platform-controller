package taskrun

import (
	"context"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

const testNamespace = "default"

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(scheme))
}

func TestTaskRunCreates(t *testing.T) {
	g := NewGomegaWithT(t)

	preexisting := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HostConfig,
			Namespace: testNamespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		Data: map[string]string{
			"host.power-rhtap-prod-1.address":  "10.130.75.23",
			"host.power-rhtap-prod-1.platform": "linux/ppc64le",
			// TODO: add the rest values
			//host.power-rhtap-prod-1.user: "root"
			//host.power-rhtap-prod-1.secret: "internal-prod-ibm-ssh-key"
			//host.power-rhtap-prod-1.concurrency: "1"

		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(preexisting).Build()
	log := logr.FromContextOrDiscard(context.TODO())

	// tested function call
	UpdateHostPools(testNamespace, k8sClient, &log)

	// get list of all TaskRuns, as we cannot predict the name
	createdList := v1.TaskRunList{}

	time.Sleep(3 * time.Second) //TODO: try to replace with Eventually() as by example below

	//Eventually(func(g Gomega) {
	//	g.Expect(k8sClient.List(context.TODO(), &createdList, client.InNamespace(testNamespace))).To(Succeed())
	//	g.Expect(len(createdList.Items)).To(Equal(1))
	//}).Should(Succeed())

	g.Expect(k8sClient.List(context.TODO(), &createdList, client.InNamespace(testNamespace))).To(Succeed())
	g.Expect(len(createdList.Items)).To(Equal(1))

	createdTR := createdList.Items[0]

	g.Expect(createdTR.Labels).To(HaveKeyWithValue(TaskTypeLabel, TaskTypeUpdate))
	//TODO: verify the rest of the important resulting TaskRun fields, ie. rest of labels, spec.params, workspaces etc
}

//TODO: try to find other scenarios to test
