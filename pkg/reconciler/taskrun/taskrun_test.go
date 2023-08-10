package taskrun

import (
	"context"
	"github.com/go-logr/logr"
	"testing"

	. "github.com/onsi/gomega"
	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const namespace = "multi-arch-operator"

func setupClientAndReconciler(objs ...runtimeclient.Object) (runtimeclient.Client, *ReconcileTaskRun) {
	scheme := runtime.NewScheme()
	_ = pipelinev1beta1.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	reconciler := &ReconcileTaskRun{client: client, scheme: scheme, eventRecorder: &record.FakeRecorder{}, operatorNamespace: namespace}
	return client, reconciler
}

func TestConfigMapParsing(t *testing.T) {

	g := NewGomegaWithT(t)
	cm := createHostConfig()
	_, reconciler := setupClientAndReconciler(&cm)
	discard := logr.Discard()
	config, err := reconciler.hostConfig(context.TODO(), &discard)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(len(config)).To(Equal(2))
	g.Expect(config["host1"].Arch).Should(Equal("arm64"))
}

func createHostConfig() v1.ConfigMap {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = namespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"host1.address":     "ec2-54-165-44-192.compute-1.amazonaws.com",
		"host1.secret":      "aws-keys",
		"host1.concurrency": "4",
		"host1.user":        "ec2-user",
		"host1.arch":        "arm64",
		"host2.address":     "ec2-34-227-115-211.compute-1.amazonaws.com",
		"host2.secret":      "aws-keys",
		"host2.concurrency": "4",
		"host2.user":        "ec2-user",
		"host2.arch":        "arm64",
	}
	return cm
}
