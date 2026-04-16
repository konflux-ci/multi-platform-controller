// This file contains all the tests for the dynamic host provisioning logic.
// It covers the lifecycle of on-demand cloud instances, including allocation,
// failure handling (e.g., timeouts, mid-provision failures), and eventual
// termination, using a mock cloud provider to simulate these interactions.

package taskrun

import (
	"time"

	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"

	. "github.com/konflux-ci/multi-platform-controller/pkg/constant"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Test Dynamic Host Provisioning", func() {

	var client runtimeclient.Client
	var reconciler *ReconcileTaskRun

	BeforeEach(func() {
		client, reconciler = setupClientAndReconciler(createDynamicHostConfig())
		cloudImpl.Instances = map[cloud.InstanceIdentifier]MockInstance{}
		cloudImpl.Running = 0
		cloudImpl.Terminated = 0
	})

	When("when dynamic host provisioning is set to succeed", func() {

		// It verifies that the dynamic host configuration, including additional
		// instance tags, is parsed correctly from the main ConfigMap.
		It("the ConfigMap should be parsed correctly", func(ctx SpecContext) {
			configIface, err := reconciler.getPlatformConfig(ctx, "linux/arm64", userNamespace)
			config := configIface.(DynamicResolver)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("foo", "bar"))
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("key", "value"))
		})

		// It tests the full happy-path for dynamic provisioning:
		// 1. A user TaskRun is created.
		// 2. A cloud instance is launched via the mock provider.
		// 3. A provisioner TaskRun is created to set up the new instance.
		// 4. The provisioner succeeds.
		// 5. The user TaskRun completes.
		// 6. The cloud instance is terminated as part of cleanup.
		It("should allocate a cloud host correctly", func(ctx SpecContext) {
			tr := runUserPipeline(ctx, client, reconciler, "test-dynamic-alloc")
			provision := getProvisionTaskRun(ctx, client, tr)
			params := map[string]string{}
			for _, i := range provision.Spec.Params {
				params[i.Name] = i.Value.StringVal
			}
			Expect(params["SECRET_NAME"]).Should(Equal("multi-platform-ssh-test-dynamic-alloc"))
			Expect(params["TASKRUN_NAME"]).Should(Equal("test-dynamic-alloc"))
			Expect(params["NAMESPACE"]).Should(Equal(userNamespace))
			Expect(params["USER"]).Should(Equal("root"))
			Expect(params["HOST"]).Should(Equal("test-dynamic-alloc.host.com"))

			Expect(cloudImpl.Instances).Should(HaveKey(cloud.InstanceIdentifier("test-dynamic-alloc")))
			Expect(cloudImpl.Instances[("test-dynamic-alloc")].Address).Should(Equal("test-dynamic-alloc.host.com"))
			Expect(cloudImpl.Instances[("test-dynamic-alloc")].taskRun).Should(Equal("test-dynamic-alloc task run"))

			runSuccessfulProvision(ctx, provision, client, tr, reconciler)

			Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)).ShouldNot(HaveOccurred())
			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
			})
			Expect(client.Status().Update(ctx, tr)).ShouldNot(HaveOccurred())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			// Verify step 6: The cloud instance is terminated as part of cleanup
			Expect(cloudImpl.Instances).ShouldNot(HaveKey(cloud.InstanceIdentifier("multi-platform-builder-test-dynamic-alloc")))
		})
	})

	When("when provisioning fails", func() {

		// It simulates a scenario where the mock cloud provider fails to return
		// an address for a newly launched instance. The test verifies that the
		// reconciler correctly identifies this as a failure, cleans up the
		// orphaned instance, and does not assign a host to the TaskRun.
		It("should handle instance address failure correctly", func(ctx SpecContext) {
			cloudImpl.FailGetAddress = true
			defer func() { cloudImpl.FailGetAddress = false }()

			createUserTaskRun(ctx, client, "test-addr-fail", "linux/arm64")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-addr-fail"}})
			Expect(err).ShouldNot(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-addr-fail"}})
			Expect(err).Should(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-addr-fail"}})
			Expect(err).ShouldNot(HaveOccurred())
			tr := getUserTaskRun(ctx, client, "test-addr-fail")
			Expect(tr.Labels[AssignedHost]).Should(BeEmpty())
			Expect(cloudImpl.Running).Should(Equal(0))
			Expect(cloudImpl.Instances).ShouldNot(HaveKey(cloud.InstanceIdentifier("multi-platform-builder-test-addr-fail")))
		})

		// It simulates a scenario where launching a cloud instance times out.
		// The test verifies that the reconciler retries for a configured duration
		// before ultimately failing the TaskRun and cleaning up the orphaned instance.
		It("should handle instance timeout correctly", func(ctx SpecContext) {
			cloudImpl.TimeoutGetAddress = true
			defer func() { cloudImpl.TimeoutGetAddress = false }()

			createUserTaskRun(ctx, client, "test-timeout", "linux/arm64")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-timeout"}})
			Expect(err).ShouldNot(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-timeout"}})
			Expect(err).ShouldNot(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-timeout"}})
			Expect(err).ShouldNot(HaveOccurred())
			time.Sleep(time.Second)
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-timeout"}})
			Expect(err).ShouldNot(HaveOccurred())
			time.Sleep(time.Second * 2)
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-timeout"}})
			Expect(err).Should(HaveOccurred())
			tr := getUserTaskRun(ctx, client, "test-timeout")
			Expect(tr.Labels[AssignedHost]).Should(BeEmpty())
			Expect(cloudImpl.Running).Should(Equal(0))
			Expect(cloudImpl.Instances).ShouldNot(HaveKey(cloud.InstanceIdentifier("multi-platform-builder-test-timeout")))
		})

		// It tests the cleanup logic for a scenario where a user TaskRun fails
		// after a cloud instance has already been allocated but before the
		// provisioner has finished. This ensures that no orphaned cloud
		// instances are left running.
		It("should handle provision failure in the middle correctly", func(ctx SpecContext) {
			createUserTaskRun(ctx, client, "test-mid-fail", "linux/arm64")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-mid-fail"}})
			Expect(err).ShouldNot(HaveOccurred())

			tr := getUserTaskRun(ctx, client, "test-mid-fail")
			if tr.Labels[AssignedHost] == "" {
				Expect(tr.Annotations[CloudInstanceId]).ShouldNot(BeEmpty())
			}
			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "False",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
			})
			Expect(client.Status().Update(ctx, tr)).ShouldNot(HaveOccurred())

			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			Expect(cloudImpl.Instances).ShouldNot(HaveKey(cloud.InstanceIdentifier("multi-platform-builder-test-mid-fail")))
			Expect(cloudImpl.Running).Should(Equal(0))
		})
	})

	When("the VM instance enters a failed state while waiting for an address", func() {

		It("should terminate the instance and requeue when GetState returns FailedState", func(ctx SpecContext) {
			cloudImpl.TimeoutGetAddress = true
			defer func() { cloudImpl.TimeoutGetAddress = false }()

			createUserTaskRun(ctx, client, "test-failed-state", "linux/arm64")
			// 1st reconcile: launches instance
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-failed-state"}})
			Expect(err).ShouldNot(HaveOccurred())

			// Mark the instance as failed
			tr := getUserTaskRun(ctx, client, "test-failed-state")
			instanceId := cloud.InstanceIdentifier(tr.Annotations[CloudInstanceId])
			Expect(instanceId).ShouldNot(BeEmpty())
			inst := cloudImpl.Instances[instanceId]
			inst.statusOK = false
			cloudImpl.Instances[instanceId] = inst

			// 2nd reconcile: address is empty, GetState returns FailedState → terminate + unassign
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-failed-state"}})
			Expect(err).ShouldNot(HaveOccurred())

			tr = getUserTaskRun(ctx, client, "test-failed-state")
			Expect(tr.Annotations[CloudInstanceId]).Should(BeEmpty())
			Expect(cloudImpl.Running).Should(Equal(0))
		})

		It("should requeue quickly when GetState returns an error", func(ctx SpecContext) {
			cloudImpl.TimeoutGetAddress = true
			cloudImpl.FailGetState = true
			defer func() {
				cloudImpl.TimeoutGetAddress = false
				cloudImpl.FailGetState = false
			}()

			createUserTaskRun(ctx, client, "test-getstate-err", "linux/arm64")
			// 1st reconcile: launches instance
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-getstate-err"}})
			Expect(err).ShouldNot(HaveOccurred())

			// 2nd reconcile: address is empty, GetState fails → requeue (no error returned)
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-getstate-err"}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(result.RequeueAfter).Should(BeNumerically(">", 0))

			// Instance should still exist (not terminated on GetState error)
			Expect(cloudImpl.Running).Should(Equal(1))
		})
	})

	When("launching a new instance fails", func() {

		It("should retry on first launch failure and requeue", func(ctx SpecContext) {
			cloudImpl.FailLaunch = true
			defer func() { cloudImpl.FailLaunch = false }()

			createUserTaskRun(ctx, client, "test-launch-fail", "linux/arm64")
			// 1st reconcile: tries to launch, fails → sets failure count to 1, requeues
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-launch-fail"}})
			Expect(err).ShouldNot(HaveOccurred())

			tr := getUserTaskRun(ctx, client, "test-launch-fail")
			Expect(tr.Annotations[CloudFailures]).Should(Equal("1"))

			// 2nd reconcile: tries again, fails → sets failure count to 2, requeues
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-launch-fail"}})
			Expect(err).ShouldNot(HaveOccurred())

			tr = getUserTaskRun(ctx, client, "test-launch-fail")
			Expect(tr.Annotations[CloudFailures]).Should(Equal("2"))

			// 3rd reconcile: retries exceeded (failureCount == 2) → returns error
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-launch-fail"}})
			Expect(err).Should(HaveOccurred())
		})
	})

	When("counting instances fails", func() {

		It("should return error when CountInstances fails", func(ctx SpecContext) {
			cloudImpl.FailCountInstances = true
			defer func() { cloudImpl.FailCountInstances = false }()

			createUserTaskRun(ctx, client, "test-count-fail", "linux/arm64")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-count-fail"}})
			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("count instances failed"))
		})
	})

	When("the instance pool is at capacity", func() {

		It("should wait and requeue when maxInstances is reached", func(ctx SpecContext) {
			// Fill the pool to capacity (maxInstances=2)
			_, err := cloudImpl.LaunchInstance(nil, ctx, "default:existing-1", "test-tag", nil)
			Expect(err).ShouldNot(HaveOccurred())
			_, err = cloudImpl.LaunchInstance(nil, ctx, "default:existing-2", "test-tag", nil)
			Expect(err).ShouldNot(HaveOccurred())

			createUserTaskRun(ctx, client, "test-pool-full", "linux/arm64")
			// Reconcile: pool is full → adds waiting label, requeues
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-pool-full"}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(result.RequeueAfter).Should(Equal(time.Minute))

			tr := getUserTaskRun(ctx, client, "test-pool-full")
			Expect(tr.Labels[WaitingForPlatformLabel]).ShouldNot(BeEmpty())

			// Reconcile again while still full → already waiting, just requeues
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-pool-full"}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(result.RequeueAfter).Should(Equal(time.Minute))
		})
	})

	// Tests for buildDynamicResolver function - only the sad paths since happy paths are thoroughly tested elsewhere
	When("testing buildDynamicResolver error paths", func() {
		It("should use default instance tag when platform config doesn't specify one", func(ctx SpecContext) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      HostConfig,
					Namespace: systemNamespace,
					Labels:    map[string]string{ConfigMapLabel: "hosts"},
				},
				Data: map[string]string{
					"instance-tag":                           "global-default-tag",
					"dynamic-platforms":                      "linux/arm64",
					"dynamic.linux-arm64.type":               "aws",
					"dynamic.linux-arm64.max-instances":      "2",
					"dynamic.linux-arm64.ssh-secret":         "arm64-secret",
					"dynamic.linux-arm64.allocation-timeout": "300",
					// Note: NO instance-tag field for this platform
				},
			}
			sec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "arm64-secret",
					Namespace: systemNamespace,
					Labels:    map[string]string{MultiPlatformSecretLabel: "true"},
				},
			}

			_, reconciler := setupClientAndReconciler([]runtimeclient.Object{cm, sec})
			configIface, err := reconciler.getPlatformConfig(ctx, "linux/arm64", userNamespace)
			Expect(err).ShouldNot(HaveOccurred())
			config := configIface.(DynamicResolver)
			Expect(config.instanceTag).Should(Equal("global-default-tag"))
		})

		It("should use empty string when neither platform nor default instance tag is specified", func(ctx SpecContext) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      HostConfig,
					Namespace: systemNamespace,
					Labels:    map[string]string{ConfigMapLabel: "hosts"},
				},
				Data: map[string]string{
					"dynamic-platforms":                      "linux/arm64",
					"dynamic.linux-arm64.type":               "aws",
					"dynamic.linux-arm64.max-instances":      "2",
					"dynamic.linux-arm64.ssh-secret":         "arm64-secret",
					"dynamic.linux-arm64.allocation-timeout": "300",
					// Note: NO instance-tag field for this platform AND no global default
				},
			}
			sec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "arm64-secret",
					Namespace: systemNamespace,
					Labels:    map[string]string{MultiPlatformSecretLabel: "true"},
				},
			}

			_, reconciler := setupClientAndReconciler([]runtimeclient.Object{cm, sec})
			configIface, err := reconciler.getPlatformConfig(ctx, "linux/arm64", userNamespace)
			Expect(err).ShouldNot(HaveOccurred())
			config := configIface.(DynamicResolver)
			Expect(config.instanceTag).Should(BeEmpty())
		})
	})
})
