package taskrun

import (
	"context"
	"fmt"
	"time"

	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"knative.dev/pkg/apis"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const systemNamespace = "multi-platform-controller"
const userNamespace = "default"

var cloudImpl MockCloud = MockCloud{Addressses: map[cloud.InstanceIdentifier]string{}}

func setupClientAndReconciler(objs []runtimeclient.Object) (runtimeclient.Client, *ReconcileTaskRun) {
	scheme := runtime.NewScheme()
	_ = pipelinev1.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	reconciler := &ReconcileTaskRun{client: client, scheme: scheme, eventRecorder: &record.FakeRecorder{}, operatorNamespace: systemNamespace, cloudProviders: map[string]func(platform string, config map[string]string, systemnamespace string) cloud.CloudProvider{"mock": MockCloudSetup}, platformConfig: map[string]PlatformConfig{}}
	return client, reconciler
}

var _ = Describe("TaskRun Reconciler Tests", func() {

	Describe("Test Config Map Parsing", func() {
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			_, reconciler = setupClientAndReconciler(createHostConfig())
		})

		It("should parse the ConfigMap correctly", func() {
			configIface, err := reconciler.readConfiguration(context.Background(), "linux/arm64", userNamespace)
			config := configIface.(HostPool)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(config.hosts)).To(Equal(2))
			Expect(config.hosts["host1"].Platform).Should(Equal("linux/arm64"))
		})
	})

	Describe("Test Config Map Parsing For Local", func() {
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			_, reconciler = setupClientAndReconciler(createLocalHostConfig())
		})

		It("should parse the ConfigMap for local correctly", func() {
			configIface, err := reconciler.readConfiguration(context.Background(), "linux/arm64", userNamespace)
			Expect(configIface).To(BeAssignableToTypeOf(Local{}))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Test Config Map Parsing For Dynamic", func() {
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			_, reconciler = setupClientAndReconciler(createDynamicHostConfig())
		})

		It("should parse the ConfigMap for dynamic correctly", func() {
			configIface, err := reconciler.readConfiguration(context.Background(), "linux/arm64", userNamespace)
			config := configIface.(DynamicResolver)
			Expect(err).ToNot(HaveOccurred())
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("foo", "bar"))
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("key", "value"))
		})
	})

	Describe("Test Config Map Parsing For Dynamic Pool", func() {
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			_, reconciler = setupClientAndReconciler(createDynamicPoolHostConfig())
		})

		It("should parse the ConfigMap for dynamic pool correctly", func() {
			configIface, err := reconciler.readConfiguration(context.Background(), "linux/arm64", userNamespace)
			config := configIface.(DynamicHostPool)
			Expect(err).ToNot(HaveOccurred())
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("foo", "bar"))
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("key", "value"))
		})
	})

	Describe("Test Allowed Namespaces", func() {
		var reconciler *ReconcileTaskRun
		var err error

		BeforeEach(func() {
			_, reconciler = setupClientAndReconciler(createHostConfig())
		})

		It("should allow the correct namespaces", func() {
			Expect(err).ToNot(HaveOccurred())
			_, err = reconciler.readConfiguration(context.Background(), "linux/arm64", "system-test")
			Expect(err).ToNot(HaveOccurred())
			_, err = reconciler.readConfiguration(context.Background(), "linux/arm64", "other")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Test Allocate Host", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createHostConfig())
		})

		It("should allocate a host correctly", func() {
			tr := runUserPipeline(GinkgoT(), client, reconciler, "test")
			provision := getProvisionTaskRun(GinkgoT(), client, tr)
			params := map[string]string{}
			for _, i := range provision.Spec.Params {
				params[i.Name] = i.Value.StringVal
			}
			Expect(params["SECRET_NAME"]).To(Equal("multi-platform-ssh-test"))
			Expect(params["TASKRUN_NAME"]).To(Equal("test"))
			Expect(params["NAMESPACE"]).To(Equal(userNamespace))
			Expect(params["USER"]).To(Equal("ec2-user"))
			Expect(params["HOST"]).Should(BeElementOf("ec2-34-227-115-211.compute-1.amazonaws.com", "ec2-54-165-44-192.compute-1.amazonaws.com"))
		})
	})

	Describe("Test Allocate Cloud Host", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createDynamicHostConfig())
		})

		It("should allocate a cloud host correctly", func() {
			tr := runUserPipeline(GinkgoT(), client, reconciler, "test")
			provision := getProvisionTaskRun(GinkgoT(), client, tr)
			params := map[string]string{}
			for _, i := range provision.Spec.Params {
				params[i.Name] = i.Value.StringVal
			}
			Expect(params["SECRET_NAME"]).To(Equal("multi-platform-ssh-test"))
			Expect(params["TASKRUN_NAME"]).To(Equal("test"))
			Expect(params["NAMESPACE"]).To(Equal(userNamespace))
			Expect(params["USER"]).To(Equal("root"))
			Expect(params["HOST"]).Should(Equal("test.host.com"))
			Expect(cloudImpl.Addressses[("test")]).Should(Equal("test.host.com"))

			runSuccessfulProvision(provision, GinkgoT(), client, tr, reconciler)

			Expect(client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)).ShouldNot(HaveOccurred())
			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
			})
			Expect(client.Update(context.Background(), tr)).ShouldNot(HaveOccurred())

			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			Expect(cloudImpl.Addressses["multi-platform-builder-test"]).Should(BeEmpty())
		})
	})

	Describe("Test Allocate Local Host", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createLocalHostConfig())
		})

		It("should allocate a local host correctly", func() {
			tr := runUserPipeline(GinkgoT(), client, reconciler, "test")
			ExpectNoProvisionTaskRun(GinkgoT(), client, tr)
			secret := getSecret(client, tr)
			Expect(secret.Data["error"]).To(BeEmpty())
			Expect(secret.Data["host"]).To(Equal([]byte("localhost")))

			// Set user task as complete - should probably factor this out from all
			// tests to a nice function at some point
			Expect(client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)).Should(Succeed())
			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
			})
			Expect(client.Update(context.Background(), tr)).Should(Succeed())

			// Run reconciler once more to trigger cleanup
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			assertNoSecret(GinkgoT(), client, tr)
		})
	})

	Describe("Test Allocate Cloud Host Instance Failure", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createDynamicHostConfig())
			cloudImpl.FailGetAddress = true
		})

		AfterEach(func() {
			cloudImpl.FailGetAddress = false
		})

		It("should handle instance failure correctly", func() {
			createUserTaskRun(GinkgoT(), client, "test", "linux/arm64")
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).ToNot(HaveOccurred())
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).To(HaveOccurred())
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).ToNot(HaveOccurred())
			tr := getUserTaskRun(GinkgoT(), client, "test")
			Expect(tr.Labels[AssignedHost]).To(BeEmpty())
			Expect(cloudImpl.Running).Should(Equal(0))
			Expect(cloudImpl.Addressses["multi-platform-builder-test"]).Should(BeEmpty())
		})
	})

	Describe("Test Allocate Cloud Host Instance Timeout", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createDynamicHostConfig())
			cloudImpl.TimeoutGetAddress = true
		})

		AfterEach(func() {
			cloudImpl.TimeoutGetAddress = false
		})

		It("should handle instance timeout correctly", func() {
			createUserTaskRun(GinkgoT(), client, "test", "linux/arm64")
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).ToNot(HaveOccurred())
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).ToNot(HaveOccurred())
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).ToNot(HaveOccurred())
			time.Sleep(time.Second)
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).ToNot(HaveOccurred())
			time.Sleep(time.Second * 2)
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).To(HaveOccurred())
			tr := getUserTaskRun(GinkgoT(), client, "test")
			Expect(tr.Labels[AssignedHost]).To(BeEmpty())
			Expect(cloudImpl.Running).Should(Equal(0))
			Expect(cloudImpl.Addressses["multi-platform-builder-test"]).Should(BeEmpty())
		})
	})

	Describe("Test Allocate Cloud Host Provision Failure In Middle", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createDynamicHostConfig())
			createUserTaskRun(GinkgoT(), client, "test", "linux/arm64")
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should handle provision failure in the middle correctly", func() {
			tr := getUserTaskRun(GinkgoT(), client, "test")
			if tr.Labels[AssignedHost] == "" {
				Expect(tr.Annotations[CloudInstanceId]).ToNot(BeEmpty())
			}
			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "False",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
			})
			Expect(client.Update(context.Background(), tr)).ShouldNot(HaveOccurred())

			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			Expect(cloudImpl.Addressses["multi-platform-builder-test"]).Should(BeEmpty())
			Expect(cloudImpl.Running).Should(Equal(0))
		})
	})

	Describe("Test Allocate Cloud Host With Dynamic Pool", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createDynamicPoolHostConfig())
		})

		It("should allocate a cloud host with dynamic pool correctly", func() {
			tr := runUserPipeline(GinkgoT(), client, reconciler, "test")
			provision := getProvisionTaskRun(GinkgoT(), client, tr)
			params := map[string]string{}
			for _, i := range provision.Spec.Params {
				params[i.Name] = i.Value.StringVal
			}
			Expect(params["SECRET_NAME"]).To(Equal("multi-platform-ssh-test"))
			Expect(params["TASKRUN_NAME"]).To(Equal("test"))
			Expect(params["NAMESPACE"]).To(Equal(userNamespace))
			Expect(params["USER"]).To(Equal("root"))
			Expect(params["HOST"]).Should(ContainSubstring(".host.com"))

			runSuccessfulProvision(provision, GinkgoT(), client, tr, reconciler)

			Expect(client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)).ShouldNot(HaveOccurred())
			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
			})
			Expect(client.Update(context.Background(), tr)).ShouldNot(HaveOccurred())

			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			Expect(len(cloudImpl.Addressses)).Should(Equal(1))
		})
	})

	Describe("Test Provision Failure", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createHostConfig())
			tr := runUserPipeline(GinkgoT(), client, reconciler, "test")
			provision := getProvisionTaskRun(GinkgoT(), client, tr)

			provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provision.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "False",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			Expect(client.Update(context.Background(), provision)).ShouldNot(HaveOccurred())

			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			tr = getUserTaskRun(GinkgoT(), client, "test")
			Expect(tr.Annotations[FailedHosts]).Should(BeElementOf("host1", "host2"))
			Expect(tr.Labels[AssignedHost]).To(Equal(""))
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			provision = getProvisionTaskRun(GinkgoT(), client, tr)

			provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provision.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "False",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			Expect(client.Update(context.Background(), provision)).ShouldNot(HaveOccurred())
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			tr = getUserTaskRun(GinkgoT(), client, "test")
			Expect(tr.Annotations[FailedHosts]).Should(ContainSubstring("host2"))
			Expect(tr.Annotations[FailedHosts]).Should(ContainSubstring("host1"))
			Expect(tr.Labels[AssignedHost]).Should(Equal(""))
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).Should(HaveOccurred())

			secret := getSecret(client, tr)
			Expect(secret.Data["error"]).ToNot(BeEmpty())
		})
	})

	Describe("Test Provision Success But No Secret", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createHostConfig())
			tr := runUserPipeline(GinkgoT(), client, reconciler, "test")
			provision := getProvisionTaskRun(GinkgoT(), client, tr)

			provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provision.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			Expect(client.Update(context.Background(), provision)).ShouldNot(HaveOccurred())

			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			secret := getSecret(client, tr)
			Expect(secret.Data["error"]).ToNot(BeEmpty())
		})
	})

	Describe("Test Provision Success", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createHostConfig())
			tr := runUserPipeline(GinkgoT(), client, reconciler, "test")
			provision := getProvisionTaskRun(GinkgoT(), client, tr)

			runSuccessfulProvision(provision, GinkgoT(), client, tr, reconciler)

			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			Expect(client.Update(context.Background(), tr)).ShouldNot(HaveOccurred())
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			assertNoSecret(GinkgoT(), client, tr)

			list := pipelinev1.TaskRunList{}
			err = client.List(context.Background(), &list)
			Expect(err).ToNot(HaveOccurred())

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
						Expect(client.Update(context.Background(), &list.Items[idx])).ShouldNot(HaveOccurred())
					}

					_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: i.Namespace, Name: i.Name}})
					Expect(err).ShouldNot(HaveOccurred())
				}
			}

			taskExists := false
			err = client.List(context.Background(), &list)
			Expect(err).ToNot(HaveOccurred())
			for _, i := range list.Items {
				if i.Labels[TaskTypeLabel] != "" {
					taskExists = true
				}
			}
			Expect(taskExists).To(BeFalse())
		})
	})

	Describe("Test Wait For Concurrency", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createHostConfig())
			runs := []*pipelinev1.TaskRun{}
			for i := 0; i < 8; i++ {
				tr := runUserPipeline(GinkgoT(), client, reconciler, fmt.Sprintf("test-%d", i))
				provision := getProvisionTaskRun(GinkgoT(), client, tr)
				runSuccessfulProvision(provision, GinkgoT(), client, tr, reconciler)
				runs = append(runs, tr)
			}
			name := fmt.Sprintf("test-%d", 9)
			createUserTaskRun(GinkgoT(), client, name, "linux/arm64")
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
			Expect(err).ToNot(HaveOccurred())
			tr := getUserTaskRun(GinkgoT(), client, name)
			Expect(tr.Labels[WaitingForPlatformLabel]).To(Equal("linux-arm64"))

			running := runs[0]
			running.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			running.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			Expect(client.Update(context.Background(), running)).ShouldNot(HaveOccurred())
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: running.Namespace, Name: running.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			assertNoSecret(GinkgoT(), client, running)

			tr = getUserTaskRun(GinkgoT(), client, name)
			Expect(tr.Labels[WaitingForPlatformLabel]).To(BeEmpty())
			_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
			Expect(err).ToNot(HaveOccurred())
			tr = getUserTaskRun(GinkgoT(), client, name)
			Expect(getProvisionTaskRun(GinkgoT(), client, tr)).ToNot(BeNil())
		})
	})

	Describe("Test No Host Config", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler([]runtimeclient.Object{})
			createUserTaskRun(GinkgoT(), client, "test", "linux/arm64")
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).ToNot(HaveOccurred())
			tr := getUserTaskRun(GinkgoT(), client, "test")

			secret := getSecret(client, tr)
			Expect(secret.Data["error"]).ToNot(BeEmpty())
		})
	})

	Describe("Test No Host With Out Platform", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(createHostConfig())
			createUserTaskRun(GinkgoT(), client, "test", "powerpc")
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test"}})
			Expect(err).To(HaveOccurred())
			tr := getUserTaskRun(GinkgoT(), client, "test")

			secret := getSecret(client, tr)
			Expect(secret.Data["error"]).ToNot(BeEmpty())
		})
	})
})

func runSuccessfulProvision(provision *pipelinev1.TaskRun, g GinkgoTInterface, client runtimeclient.Client, tr *pipelinev1.TaskRun, reconciler *ReconcileTaskRun) {
	provision.Status.CompletionTime = &metav1.Time{Time: time.Now().Add(time.Hour * -2)}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
	})
	Expect(client.Update(context.Background(), provision)).ShouldNot(HaveOccurred())

	s := v1.Secret{}
	s.Name = SecretPrefix + tr.Name
	s.Namespace = tr.Namespace
	s.Data = map[string][]byte{}
	s.Data["id_rsa"] = []byte("expected")
	s.Data["host"] = []byte("host")
	s.Data["user-dir"] = []byte("buildir")
	Expect(client.Create(context.Background(), &s)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	Expect(err).ShouldNot(HaveOccurred())
	secret := getSecret(client, tr)
	Expect(secret.Data["error"]).To(BeEmpty())
}

func getSecret(client runtimeclient.Client, tr *pipelinev1.TaskRun) *v1.Secret {
	name := SecretPrefix + tr.Name
	secret := v1.Secret{}
	Expect(client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: name}, &secret)).To(Succeed())
	return &secret
}

func assertNoSecret(g GinkgoTInterface, client runtimeclient.Client, tr *pipelinev1.TaskRun) {
	name := SecretPrefix + tr.Name
	secret := v1.Secret{}
	err := client.Get(context.Background(), types.NamespacedName{Namespace: tr.Namespace, Name: name}, &secret)
	Expect(errors.IsNotFound(err)).To(BeTrue())
}

func runUserPipeline(g GinkgoTInterface, client runtimeclient.Client, reconciler *ReconcileTaskRun, name string) *pipelinev1.TaskRun {
	createUserTaskRun(g, client, name, "linux/arm64")
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	Expect(err).ToNot(HaveOccurred())
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	Expect(err).ToNot(HaveOccurred())
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	Expect(err).ToNot(HaveOccurred())
	tr := getUserTaskRun(g, client, name)
	if tr.Labels[AssignedHost] == "" {
		Expect(tr.Annotations[CloudInstanceId]).ToNot(BeEmpty())
		_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
		Expect(err).ToNot(HaveOccurred())
		tr = getUserTaskRun(g, client, name)
	}
	Expect(tr.Labels[AssignedHost]).ToNot(BeEmpty())
	return tr
}

func getProvisionTaskRun(g GinkgoTInterface, client runtimeclient.Client, tr *pipelinev1.TaskRun) *pipelinev1.TaskRun {
	list := pipelinev1.TaskRunList{}
	err := client.List(context.Background(), &list)
	Expect(err).ToNot(HaveOccurred())
	for i := range list.Items {
		if list.Items[i].Labels[AssignedHost] == "" {
			continue
		}
		if list.Items[i].Labels[UserTaskName] == tr.Name {
			return &list.Items[i]
		}
	}
	Expect("could not find task").Should(BeEmpty())
	return nil
}

func ExpectNoProvisionTaskRun(g GinkgoTInterface, client runtimeclient.Client, tr *pipelinev1.TaskRun) {
	list := pipelinev1.TaskRunList{}
	err := client.List(context.Background(), &list)
	Expect(err).ToNot(HaveOccurred())
	foundCount := 0
	for i := range list.Items {
		if list.Items[i].Labels[AssignedHost] == "" && list.Items[i].Labels[UserTaskName] == tr.Name {
			foundCount++
		}
	}
	Expect(foundCount).Should(BeNumerically("==", 0))
}

func getUserTaskRun(g GinkgoTInterface, client runtimeclient.Client, name string) *pipelinev1.TaskRun {
	ret := pipelinev1.TaskRun{}
	err := client.Get(context.Background(), types.NamespacedName{Namespace: userNamespace, Name: name}, &ret)
	Expect(err).ToNot(HaveOccurred())
	return &ret
}

func createUserTaskRun(g GinkgoTInterface, client runtimeclient.Client, name string, platform string) {
	tr := &pipelinev1.TaskRun{}
	tr.Namespace = userNamespace
	tr.Name = name
	tr.Spec = pipelinev1.TaskRunSpec{
		Params: []pipelinev1.Param{{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues(platform)}},
	}
	tr.Status.TaskSpec = &pipelinev1.TaskSpec{Volumes: []v1.Volume{{Name: "test", VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: SecretPrefix + name}}}}}
	Expect(client.Create(context.Background(), tr)).ToNot(HaveOccurred())
}

func createHostConfig() []runtimeclient.Object {
	cm := createHostConfigMap()
	sec := v1.Secret{}
	sec.Name = "awskeys"
	sec.Namespace = systemNamespace
	sec.Labels = map[string]string{MultiPlatformSecretLabel: "true"}
	return []runtimeclient.Object{&cm, &sec}
}

func createHostConfigMap() v1.ConfigMap {
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
	return cm
}

func createDynamicHostConfig() []runtimeclient.Object {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"additional-instance-tags":               "foo=bar,key=value",
		"dynamic-platforms":                      "linux/arm64",
		"dynamic.linux-arm64.type":               "mock",
		"dynamic.linux-arm64.region":             "us-east-1",
		"dynamic.linux-arm64.ami":                "ami-03d6a5256a46c9feb",
		"dynamic.linux-arm64.instance-type":      "t4g.medium",
		"dynamic.linux-arm64.key-name":           "sdouglas-arm-test",
		"dynamic.linux-arm64.aws-secret":         "awsiam",
		"dynamic.linux-arm64.ssh-secret":         "awskeys",
		"dynamic.linux-arm64.max-instances":      "2",
		"dynamic.linux-arm64.allocation-timeout": "2",
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
		"additional-instance-tags":          "foo=bar,key=value",
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

func createLocalHostConfig() []runtimeclient.Object {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"local-platforms": "linux/arm64",
	}
	return []runtimeclient.Object{&cm}
}

type MockCloud struct {
	Running           int
	Terminated        int
	Addressses        map[cloud.InstanceIdentifier]string
	FailGetAddress    bool
	TimeoutGetAddress bool
}

func (m *MockCloud) ListInstances(kubeClient runtimeclient.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	ret := []cloud.CloudVMInstance{}
	for k, v := range m.Addressses {
		ret = append(ret, cloud.CloudVMInstance{InstanceId: k, StartTime: time.Now(), Address: v})
	}
	return ret, nil
}

func (m *MockCloud) CountInstances(kubeClient runtimeclient.Client, ctx context.Context, instanceTag string) (int, error) {
	return m.Running, nil
}

func (m *MockCloud) SshUser() string {
	return "root"
}

func (m *MockCloud) LaunchInstance(kubeClient runtimeclient.Client, ctx context.Context, name string, instanceTag string, additionalTags map[string]string) (cloud.InstanceIdentifier, error) {
	m.Running++
	addr := string(name) + ".host.com"
	identifier := cloud.InstanceIdentifier(name)
	m.Addressses[identifier] = addr
	return identifier, nil
}

func (m *MockCloud) TerminateInstance(kubeClient runtimeclient.Client, ctx context.Context, instance cloud.InstanceIdentifier) error {
	m.Running--
	m.Terminated++
	delete(m.Addressses, instance)
	return nil
}

func (m *MockCloud) GetInstanceAddress(kubeClient runtimeclient.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	if m.FailGetAddress {
		return "", fmt.Errorf("failed")
	} else if m.TimeoutGetAddress {
		return "", nil
	}
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
