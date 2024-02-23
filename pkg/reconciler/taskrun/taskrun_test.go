package taskrun

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/redhat-appstudio/multi-platform-controller/pkg/cloud"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const systemNamespace = "multi-platform-controller"
const userNamespace = "default"

var cloudImpl MockCloud = MockCloud{Addressses: map[cloud.InstanceIdentifier]string{}}
var platformMetrics = map[string]*PlatformMetrics{}

func setupClientAndReconciler(objs []runtimeclient.Object) (runtimeclient.Client, *ReconcileTaskRun) {
	scheme := runtime.NewScheme()
	_ = pipelinev1.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	reconciler := &ReconcileTaskRun{client: client, scheme: scheme, eventRecorder: &record.FakeRecorder{}, operatorNamespace: systemNamespace, cloudProviders: map[string]func(platform string, config map[string]string, systemnamespace string) cloud.CloudProvider{"mock": MockCloudSetup}, platformConfig: map[string]PlatformConfig{}, platformMetrics: platformMetrics}
	return client, reconciler
}

func TestConfigMapParsing(t *testing.T) {
	g := NewGomegaWithT(t)
	_, reconciler := setupClientAndReconciler(createHostConfig())
	discard := logr.Discard()
	configIface, err := reconciler.readConfiguration(context.Background(), &discard, "linux/arm64", userNamespace)
	config := configIface.(HostPool)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(len(config.hosts)).To(Equal(2))
	g.Expect(config.hosts["host1"].Platform).Should(Equal("linux/arm64"))
}

func TestAllowedNamepsaces(t *testing.T) {
	g := NewGomegaWithT(t)
	_, reconciler := setupClientAndReconciler(createHostConfig())
	discard := logr.Discard()
	_, err := reconciler.readConfiguration(context.Background(), &discard, "linux/arm64", "system-test")
	g.Expect(err).ToNot(HaveOccurred())
	_, err = reconciler.readConfiguration(context.Background(), &discard, "linux/arm64", "other")
	g.Expect(err).To(HaveOccurred())

}
func TestAllocateHost(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createHostConfig())

	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)
	params := map[string]string{}
	for _, i := range provision.Spec.Params {
		params[i.Name] = i.Value.StringVal
	}
	g.Expect(params["SECRET_NAME"]).To(Equal("multi-platform-ssh-test"))
	g.Expect(params["TASKRUN_NAME"]).To(Equal("test"))
	g.Expect(params["NAMESPACE"]).To(Equal(userNamespace))
	g.Expect(params["USER"]).To(Equal("ec2-user"))
	g.Expect(params["HOST"]).Should(BeElementOf("ec2-34-227-115-211.compute-1.amazonaws.com", "ec2-54-165-44-192.compute-1.amazonaws.com"))
}

func TestAllocateCloudHost(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createDynamicHostConfig())

	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)
	params := map[string]string{}
	for _, i := range provision.Spec.Params {
		params[i.Name] = i.Value.StringVal
	}
	g.Expect(params["SECRET_NAME"]).To(Equal("multi-platform-ssh-test"))
	g.Expect(params["TASKRUN_NAME"]).To(Equal("test"))
	g.Expect(params["NAMESPACE"]).To(Equal(userNamespace))
	g.Expect(params["USER"]).To(Equal("root"))
	g.Expect(params["HOST"]).Should(Equal("test.host.com"))
	g.Expect(cloudImpl.Addressses[("test")]).Should(Equal("test.host.com"))

	runSuccessfulProvision(provision, g, client, tr, reconciler)

	g.Expect(client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)).ShouldNot(HaveOccurred())
	tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	tr.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
	})
	g.Expect(client.Update(context.Background(), tr)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())

	g.Expect(cloudImpl.Addressses["multi-platform-builder-test"]).Should(BeEmpty())

}

func TestAllocateCloudHostProvisionFailureInMiddle(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createDynamicHostConfig())
	createUserTaskRun(g, client, "test", "linux/arm64")
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
	g.Expect(err).ToNot(HaveOccurred())
	tr := getUserTaskRun(g, client, "test")
	if tr.Labels[AssignedHost] == "" {
		g.Expect(tr.Annotations[CloudInstanceId]).ToNot(BeEmpty())
	}
	//now fail the task
	g.Expect(cloudImpl.Running).Should(Equal(1))

	tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	tr.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "False",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
	})
	g.Expect(client.Update(context.Background(), tr)).ShouldNot(HaveOccurred())

	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())

	g.Expect(cloudImpl.Addressses["multi-platform-builder-test"]).Should(BeEmpty())
	g.Expect(cloudImpl.Running).Should(Equal(0))
}

func TestAllocateCloudHostWithDynamicPool(t *testing.T) {
	println("HOO")
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createDynamicPoolHostConfig())

	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)
	params := map[string]string{}
	for _, i := range provision.Spec.Params {
		params[i.Name] = i.Value.StringVal
	}
	g.Expect(params["SECRET_NAME"]).To(Equal("multi-platform-ssh-test"))
	g.Expect(params["TASKRUN_NAME"]).To(Equal("test"))
	g.Expect(params["NAMESPACE"]).To(Equal(userNamespace))
	g.Expect(params["USER"]).To(Equal("root"))
	g.Expect(params["HOST"]).Should(ContainSubstring(".host.com"))

	runSuccessfulProvision(provision, g, client, tr, reconciler)

	g.Expect(client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)).ShouldNot(HaveOccurred())
	tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	tr.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
	})
	g.Expect(client.Update(context.Background(), tr)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())

	g.Expect(len(cloudImpl.Addressses)).Should(Equal(1))

}

func TestProvisionFailure(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createHostConfig())
	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)

	provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "False",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
	})
	g.Expect(client.Update(context.Background(), provision)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())

	tr = getUserTaskRun(g, client, "test")
	g.Expect(tr.Annotations[FailedHosts]).Should(BeElementOf("host1", "host2"))
	g.Expect(tr.Labels[AssignedHost]).Should(Equal(""))
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())

	provision = getProvisionTaskRun(g, client, tr)

	provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "False",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
	})
	g.Expect(client.Update(context.Background(), provision)).ShouldNot(HaveOccurred())
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())

	tr = getUserTaskRun(g, client, "test")
	g.Expect(tr.Annotations[FailedHosts]).Should(ContainSubstring("host2"))
	g.Expect(tr.Annotations[FailedHosts]).Should(ContainSubstring("host1"))
	g.Expect(tr.Labels[AssignedHost]).Should(Equal(""))
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
	g.Expect(err).Should(HaveOccurred())

	secret := getSecret(g, client, tr)
	g.Expect(secret.Data["error"]).ToNot(BeEmpty())
}

func TestProvisionSuccessButNoSecret(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createHostConfig())
	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)

	provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
	})
	g.Expect(client.Update(context.Background(), provision)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())
	secret := getSecret(g, client, tr)
	g.Expect(secret.Data["error"]).ToNot(BeEmpty())
}

func TestProvisionSuccess(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createHostConfig())
	tr := runUserPipeline(g, client, reconciler, "test")
	provision := getProvisionTaskRun(g, client, tr)

	runSuccessfulProvision(provision, g, client, tr, reconciler)

	//now test clean up
	tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	tr.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
	})
	g.Expect(client.Update(context.Background(), tr)).ShouldNot(HaveOccurred())
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())
	assertNoSecret(g, client, tr)

	//make sure the task runs were cleaned up
	list := pipelinev1.TaskRunList{}
	err = client.List(context.Background(), &list)
	g.Expect(err).ToNot(HaveOccurred())
	//reconcile the provision/cleanup tasks, which should delete them
	for idx, i := range list.Items {
		if i.Labels[TaskTypeLabel] != "" {
			if i.Status.CompletionTime == nil {
				endTime := time.Now().Add(time.Hour * -2)
				list.Items[idx].Status.CompletionTime = &metav1.Time{Time: endTime}
				list.Items[idx].Status.SetCondition(&apis.Condition{
					Type:               apis.ConditionSucceeded,
					Status:             "True",
					LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: endTime}},
				})
				g.Expect(client.Update(context.Background(), &list.Items[idx])).ShouldNot(HaveOccurred())
			}

			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: i.Namespace, Name: i.Name}})
			g.Expect(err).ShouldNot(HaveOccurred())
		}
	}
	//make sure they are gone
	taskExists := false
	err = client.List(context.Background(), &list)
	g.Expect(err).ToNot(HaveOccurred())
	for _, i := range list.Items {
		if i.Labels[TaskTypeLabel] != "" {
			taskExists = true
		}
	}
	g.Expect(taskExists).To(BeFalse())

}

func TestWaitForConcurrency(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createHostConfig())
	runs := []*pipelinev1.TaskRun{}
	for i := 0; i < 8; i++ {
		tr := runUserPipeline(g, client, reconciler, fmt.Sprintf("test-%d", i))
		provision := getProvisionTaskRun(g, client, tr)
		runSuccessfulProvision(provision, g, client, tr, reconciler)
		runs = append(runs, tr)
	}
	//we are now at max concurrency
	name := fmt.Sprintf("test-%d", 9)
	createUserTaskRun(g, client, name, "linux/arm64")
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	g.Expect(err).ToNot(HaveOccurred())
	tr := getUserTaskRun(g, client, name)
	g.Expect(tr.Labels[WaitingForPlatformLabel]).To(Equal("linux-arm64"))

	//now complete a task
	//now test clean up
	running := runs[0]
	running.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	running.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
	})
	g.Expect(client.Update(context.Background(), running)).ShouldNot(HaveOccurred())
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: running.Namespace, Name: running.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())
	assertNoSecret(g, client, running)

	//task is completed, this should have removed the waiting label from our existing task

	tr = getUserTaskRun(g, client, name)
	g.Expect(tr.Labels[WaitingForPlatformLabel]).To(BeEmpty())
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	g.Expect(err).ToNot(HaveOccurred())
	tr = getUserTaskRun(g, client, name)
	g.Expect(getProvisionTaskRun(g, client, tr)).ToNot(BeNil())
}

func runSuccessfulProvision(provision *pipelinev1.TaskRun, g *WithT, client runtimeclient.Client, tr *pipelinev1.TaskRun, reconciler *ReconcileTaskRun) {
	provision.Status.CompletionTime = &metav1.Time{Time: time.Now().Add(time.Hour * -2)}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
	})
	g.Expect(client.Update(context.Background(), provision)).ShouldNot(HaveOccurred())

	s := v1.Secret{}
	s.Name = SecretPrefix + tr.Name
	s.Namespace = tr.Namespace
	s.Data = map[string][]byte{}
	s.Data["id_rsa"] = []byte("expected")
	s.Data["host"] = []byte("host")
	s.Data["user-dir"] = []byte("buildir")
	g.Expect(client.Create(context.Background(), &s)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	g.Expect(err).ShouldNot(HaveOccurred())
	secret := getSecret(g, client, tr)
	g.Expect(secret.Data["error"]).To(BeEmpty())
}

func TestNoHostConfig(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler([]runtimeclient.Object{})
	createUserTaskRun(g, client, "test", "linux/arm64")
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
	g.Expect(err).ToNot(HaveOccurred())
	tr := getUserTaskRun(g, client, "test")

	//we should have an error secret created immediately
	secret := getSecret(g, client, tr)
	g.Expect(secret.Data["error"]).ToNot(BeEmpty())
}
func TestNoHostWithOutPlatform(t *testing.T) {
	g := NewGomegaWithT(t)
	client, reconciler := setupClientAndReconciler(createHostConfig())
	createUserTaskRun(g, client, "test", "powerpc")
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
	g.Expect(err).To(HaveOccurred())
	tr := getUserTaskRun(g, client, "test")

	//we should have an error secret created immediately
	secret := getSecret(g, client, tr)
	g.Expect(secret.Data["error"]).ToNot(BeEmpty())
}

func getSecret(g *WithT, client runtimeclient.Client, tr *pipelinev1.TaskRun) *v1.Secret {
	name := SecretPrefix + tr.Name
	secret := v1.Secret{}
	g.Expect(client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: name}, &secret)).ToNot(HaveOccurred())
	return &secret
}

func assertNoSecret(g *WithT, client runtimeclient.Client, tr *pipelinev1.TaskRun) {
	name := SecretPrefix + tr.Name
	secret := v1.Secret{}
	err := client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: name}, &secret)
	g.Expect(errors.IsNotFound(err)).To(BeTrue())
}
func runUserPipeline(g *WithT, client runtimeclient.Client, reconciler *ReconcileTaskRun, name string) *pipelinev1.TaskRun {
	createUserTaskRun(g, client, name, "linux/arm64")
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	g.Expect(err).ToNot(HaveOccurred())
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	g.Expect(err).ToNot(HaveOccurred())
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	g.Expect(err).ToNot(HaveOccurred())
	tr := getUserTaskRun(g, client, name)
	if tr.Labels[AssignedHost] == "" {
		g.Expect(tr.Annotations[CloudInstanceId]).ToNot(BeEmpty())
		_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
		g.Expect(err).ToNot(HaveOccurred())
		tr = getUserTaskRun(g, client, name)
	}
	g.Expect(tr.Labels[AssignedHost]).ToNot(BeEmpty())
	return tr
}

func getProvisionTaskRun(g *WithT, client runtimeclient.Client, tr *pipelinev1.TaskRun) *pipelinev1.TaskRun {
	list := pipelinev1.TaskRunList{}
	err := client.List(context.Background(), &list)
	g.Expect(err).ToNot(HaveOccurred())
	for i := range list.Items {
		if list.Items[i].Labels[AssignedHost] == "" {
			continue
		}
		if list.Items[i].Labels[UserTaskName] == tr.Name {
			return &list.Items[i]
		}
	}
	g.Expect("could not find task").Should(BeEmpty())
	return nil
}

func getUserTaskRun(g *WithT, client runtimeclient.Client, name string) *pipelinev1.TaskRun {
	ret := pipelinev1.TaskRun{}
	err := client.Get(context.Background(), types.NamespacedName{Namespace: userNamespace, Name: name}, &ret)
	g.Expect(err).ToNot(HaveOccurred())
	return &ret
}

func createUserTaskRun(g *WithT, client runtimeclient.Client, name string, platform string) {
	tr := &pipelinev1.TaskRun{}
	tr.Namespace = userNamespace
	tr.Name = name
	tr.Spec = pipelinev1.TaskRunSpec{
		Params: []pipelinev1.Param{{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues(platform)}},
	}
	tr.Status.TaskSpec = &pipelinev1.TaskSpec{Volumes: []v1.Volume{{Name: "test", VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: SecretPrefix + name}}}}}
	g.Expect(client.Create(context.Background(), tr)).ToNot(HaveOccurred())

}

func createHostConfig() []runtimeclient.Object {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"allowed-namespaces":     "default,system-.*",
		"host.host1.address":     "ec2-54-165-44-192.compute-1.amazonaws.com",
		"host.host1.secret":      "awskeys",
		"host.host1.concurrency": "4",
		"host.host1.user":        "ec2-user",
		"host.host1.platform":    "linux/arm64",
		"host.host2.address":     "ec2-34-227-115-211.compute-1.amazonaws.com",
		"host.host2.secret":      "awskeys",
		"host.host2.concurrency": "4",
		"host.host2.user":        "ec2-user",
		"host.host2.platform":    "linux/arm64",
	}
	sec := v1.Secret{}
	sec.Name = "awskeys"
	sec.Namespace = systemNamespace
	sec.Labels = map[string]string{MultiPlatformSecretLabel: "true"}
	return []runtimeclient.Object{&cm, &sec}
}

func createDynamicHostConfig() []runtimeclient.Object {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"dynamic-platforms":                 "linux/arm64",
		"dynamic.linux-arm64.type":          "mock",
		"dynamic.linux-arm64.region":        "us-east-1",
		"dynamic.linux-arm64.ami":           "ami-03d6a5256a46c9feb",
		"dynamic.linux-arm64.instance-type": "t4g.medium",
		"dynamic.linux-arm64.key-name":      "sdouglas-arm-test",
		"dynamic.linux-arm64.aws-secret":    "awsiam",
		"dynamic.linux-arm64.ssh-secret":    "awskeys",
		"dynamic.linux-arm64.max-instances": "2",
	}
	sec := v1.Secret{}
	sec.Name = "awskeys"
	sec.Namespace = systemNamespace
	sec.Labels = map[string]string{MultiPlatformSecretLabel: "true"}
	return []runtimeclient.Object{&cm, &sec}
}

func createDynamicPoolHostConfig() []runtimeclient.Object {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"dynamic-pool-platforms":            "linux/arm64",
		"dynamic.linux-arm64.type":          "mock",
		"dynamic.linux-arm64.region":        "us-east-1",
		"dynamic.linux-arm64.ami":           "ami-03d6a5256a46c9feb",
		"dynamic.linux-arm64.instance-type": "t4g.medium",
		"dynamic.linux-arm64.key-name":      "sdouglas-arm-test",
		"dynamic.linux-arm64.aws-secret":    "awsiam",
		"dynamic.linux-arm64.ssh-secret":    "awskeys",
		"dynamic.linux-arm64.max-instances": "2",
		"dynamic.linux-arm64.concurrency":   "2",
		"dynamic.linux-arm64.max-age":       "20",
	}
	sec := v1.Secret{}
	sec.Name = "awskeys"
	sec.Namespace = systemNamespace
	sec.Labels = map[string]string{MultiPlatformSecretLabel: "true"}
	return []runtimeclient.Object{&cm, &sec}
}

type MockCloud struct {
	Running    int
	Terminated int
	Addressses map[cloud.InstanceIdentifier]string
}

func (m *MockCloud) ListInstances(kubeClient runtimeclient.Client, log *logr.Logger, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	ret := []cloud.CloudVMInstance{}
	for k, v := range m.Addressses {
		ret = append(ret, cloud.CloudVMInstance{InstanceId: k, StartTime: time.Now(), Address: v})
	}
	return ret, nil
}

func (m *MockCloud) CountInstances(kubeClient runtimeclient.Client, log *logr.Logger, ctx context.Context, instanceTag string) (int, error) {
	return m.Running, nil
}

func (m *MockCloud) SshUser() string {
	return "root"
}

func (m *MockCloud) LaunchInstance(kubeClient runtimeclient.Client, log *logr.Logger, ctx context.Context, name string, instanceTag string) (cloud.InstanceIdentifier, error) {
	m.Running++
	addr := string(name) + ".host.com"
	identifier := cloud.InstanceIdentifier(name)
	m.Addressses[identifier] = addr
	return identifier, nil
}

func (m *MockCloud) TerminateInstance(kubeClient runtimeclient.Client, log *logr.Logger, ctx context.Context, instance cloud.InstanceIdentifier) error {
	m.Running--
	m.Terminated++
	delete(m.Addressses, instance)
	return nil
}

func (m *MockCloud) GetInstanceAddress(kubeClient runtimeclient.Client, log *logr.Logger, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	addr := m.Addressses[instanceId]
	if addr == "" {
		addr = string(instanceId) + ".host.com"
		m.Addressses[instanceId] = addr
	}
	return addr, nil
}

func MockCloudSetup(platform string, data map[string]string, systemnamespace string) cloud.CloudProvider {
	return &cloudImpl
}
