package taskrun

import (
	"context"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const systemNamespace = "multi-arch-controller"
const userNamespace = "default"

func setupClientAndReconciler(objs ...runtimeclient.Object) (runtimeclient.Client, *ReconcileTaskRun) {
	scheme := runtime.NewScheme()
	_ = pipelinev1beta1.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	reconciler := &ReconcileTaskRun{client: client, scheme: scheme, eventRecorder: &record.FakeRecorder{}, operatorNamespace: systemNamespace}
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

func TestAllocateHost(t *testing.T) {
	g := NewGomegaWithT(t)
	cm := createHostConfig()
	client, reconciler := setupClientAndReconciler(&cm)

	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)
	params := map[string]string{}
	for _, i := range provision.Spec.Params {
		params[i.Name] = i.Value.StringVal
	}
	g.Expect(params["SECRET_NAME"]).To(Equal("multi-arch-ssh-test"))
	g.Expect(params["TASKRUN_NAME"]).To(Equal("test"))
	g.Expect(params["NAMESPACE"]).To(Equal(userNamespace))
	g.Expect(params["USER"]).To(Equal("ec2-user"))
	g.Expect(params["HOST"]).Should(BeElementOf("ec2-34-227-115-211.compute-1.amazonaws.com", "ec2-54-165-44-192.compute-1.amazonaws.com"))
}

func TestProvisionFailure(t *testing.T) {
	g := NewGomegaWithT(t)
	cm := createHostConfig()
	client, reconciler := setupClientAndReconciler(&cm)
	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)

	provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "False",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
	})
	g.Expect(client.Update(context.TODO(), provision)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())
	secret := getSecret(g, client, tr)
	g.Expect(secret.Data["error"]).ToNot(BeEmpty())
}

func TestProvisionSuccessButNoSecret(t *testing.T) {
	g := NewGomegaWithT(t)
	cm := createHostConfig()
	client, reconciler := setupClientAndReconciler(&cm)
	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)

	provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
	})
	g.Expect(client.Update(context.TODO(), provision)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())
	secret := getSecret(g, client, tr)
	g.Expect(secret.Data["error"]).ToNot(BeEmpty())
}

func TestProvisionSuccess(t *testing.T) {
	g := NewGomegaWithT(t)
	cm := createHostConfig()
	client, reconciler := setupClientAndReconciler(&cm)
	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)

	provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
	})
	g.Expect(client.Update(context.TODO(), provision)).ShouldNot(HaveOccurred())

	s := v1.Secret{}
	s.Name = SecretPrefix + tr.Name
	s.Namespace = tr.Namespace
	s.Data = map[string][]byte{}
	s.Data["id_rsa"] = []byte("expected")
	s.Data["host"] = []byte("host")
	s.Data["build-dir"] = []byte("buildir")
	g.Expect(client.Create(context.TODO(), &s)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())
	secret := getSecret(g, client, tr)
	g.Expect(secret.Data["error"]).To(BeEmpty())
}
func getSecret(g *WithT, client runtimeclient.Client, tr *pipelinev1beta1.TaskRun) *v1.Secret {
	name := SecretPrefix + tr.Name
	secret := v1.Secret{}
	g.Expect(client.Get(context.TODO(), types.NamespacedName{Namespace: tr.Namespace, Name: name}, &secret)).ToNot(HaveOccurred())
	return &secret
}
func runUserPipeline(g *WithT, client runtimeclient.Client, reconciler *ReconcileTaskRun, name string) *pipelinev1beta1.TaskRun {
	tr := createUserTaskRun(g, client, name, "arm64")
	_, err := reconciler.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
	g.Expect(err).ToNot(HaveOccurred())
	tr = getUserTaskRun(g, client, "test")
	g.Expect(tr.Labels[AssignedHost]).ToNot(BeEmpty())
	g.Expect(tr.Labels[ProvisionTaskName]).ToNot(BeEmpty())
	return tr
}

func getProvisionTaskRun(g *WithT, client runtimeclient.Client, tr *pipelinev1beta1.TaskRun) *pipelinev1beta1.TaskRun {
	ret := pipelinev1beta1.TaskRun{}
	err := client.Get(context.TODO(), types.NamespacedName{Namespace: systemNamespace, Name: tr.Labels[ProvisionTaskName]}, &ret)
	g.Expect(err).ToNot(HaveOccurred())
	return &ret
}

func getUserTaskRun(g *WithT, client runtimeclient.Client, name string) *pipelinev1beta1.TaskRun {
	ret := pipelinev1beta1.TaskRun{}
	err := client.Get(context.TODO(), types.NamespacedName{Namespace: userNamespace, Name: name}, &ret)
	g.Expect(err).ToNot(HaveOccurred())
	return &ret
}

func createUserTaskRun(g *WithT, client runtimeclient.Client, name string, arch string) *pipelinev1beta1.TaskRun {
	tr := &pipelinev1beta1.TaskRun{}
	tr.Namespace = userNamespace
	tr.Name = name
	tr.Labels = map[string]string{MultiArchLabel: "true", TargetArchitectureLabel: arch}
	g.Expect(client.Create(context.TODO(), tr)).ToNot(HaveOccurred())
	return tr
}

func createHostConfig() v1.ConfigMap {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"host1.address":     "ec2-54-165-44-192.compute-1.amazonaws.com",
		"host1.secret":      "awskeys",
		"host1.concurrency": "4",
		"host1.user":        "ec2-user",
		"host1.arch":        "arm64",
		"host2.address":     "ec2-34-227-115-211.compute-1.amazonaws.com",
		"host2.secret":      "awskeys",
		"host2.concurrency": "4",
		"host2.user":        "ec2-user",
		"host2.arch":        "arm64",
	}
	return cm
}
