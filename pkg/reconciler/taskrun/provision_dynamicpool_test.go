// This file contains tests for the dynamic host pool provisioning strategy.
// It validates the management of a warm pool of running instances, including
// allocation from the pool, failure handling (e.g., an instance from the pool
// fails and must be replaced), and ensuring the pool maintains its desired capacity.

package taskrun

import (
	"time"

	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"

	. "github.com/konflux-ci/multi-platform-controller/pkg/constant"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Test Dynamic Pool Host Provisioning", func() {

	var client runtimeclient.Client
	var reconciler *ReconcileTaskRun

	BeforeEach(func() {
		client, reconciler = setupClientAndReconciler(createDynamicPoolHostConfig())
		// Clear out any instances from previous tests
		cloudImpl.Instances = map[cloud.InstanceIdentifier]MockInstance{}
		cloudImpl.Running = 0
		cloudImpl.Terminated = 0
	})

	// It tests the happy path for the dynamic pool: a TaskRun is created,
	// it gets assigned an instance from the pool, the provisioner succeeds,
	// and after completion, the instance is returned to the pool (not terminated).
	It("should allocate a cloud host with dynamic pool correctly", func(ctx SpecContext) {
		tr := runUserPipeline(ctx, client, reconciler, "test-dyn-pool")
		provision := getProvisionTaskRun(ctx, client, tr)
		params := map[string]string{}
		for _, i := range provision.Spec.Params {
			params[i.Name] = i.Value.StringVal
		}
		Expect(params["SECRET_NAME"]).Should(Equal("multi-platform-ssh-test-dyn-pool"))
		Expect(params["TASKRUN_NAME"]).Should(Equal("test-dyn-pool"))
		Expect(params["NAMESPACE"]).Should(Equal(userNamespace))
		Expect(params["USER"]).Should(Equal("root"))
		Expect(params["HOST"]).Should(ContainSubstring(".host.com"))

		runSuccessfulProvision(ctx, provision, client, tr, reconciler)

		Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)).ShouldNot(HaveOccurred())
		tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
		tr.Status.SetCondition(&apis.Condition{
			Type:               apis.ConditionSucceeded,
			Status:             "True",
			LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
		})
		Expect(client.Status().Update(ctx, tr)).Should(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
		Expect(err).ShouldNot(HaveOccurred())

		Expect(cloudImpl.Instances).Should(HaveLen(1))
	})

	When("when provisioning fails", func() {

		// It tests the resilience of the dynamic pool. When a TaskRun is assigned
		// an instance from the pool and that instance fails provisioning, this test
		// verifies that the system will attempt to launch a new instance to
		// replace the failed one, ensuring the pool maintains its desired capacity.
		It("should try to launch a new instance if an existing one fails", func(ctx SpecContext) {
			// Start with one running instance in the pool
			_, err := cloudImpl.LaunchInstance(nil, ctx, "default:preexisting-task", "multi-platform-controller", nil)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(cloudImpl.Instances).Should(HaveLen(1))

			// Start a user task. It should be assigned the single pre-existing instance.
			userTask := runUserPipeline(ctx, client, reconciler, "test-dyn-pool-fail-1")
			initialHost := userTask.Labels[AssignedHost]
			Expect(initialHost).Should(Equal("preexisting-task"))

			// Fail the provision task for that instance
			provisionTask := getProvisionTaskRun(ctx, client, userTask)
			provisionTask.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provisionTask.Status.SetCondition(&apis.Condition{
				Type:   apis.ConditionSucceeded,
				Status: v1.ConditionFalse,
			})
			Expect(client.Status().Update(ctx, provisionTask)).Should(Succeed())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provisionTask.Namespace, Name: provisionTask.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(client.Delete(ctx, provisionTask)).Should(Succeed())

			// The user task should now be un-assigned and waiting
			updatedUserTask := getUserTaskRun(ctx, client, "test-dyn-pool-fail-1")
			Expect(updatedUserTask.Annotations[FailedHosts]).Should(ContainSubstring(initialHost))
			Expect(updatedUserTask.Labels[AssignedHost]).Should(BeEmpty())

			// Reconcile the user task again. Since the pool has capacity (max 2), it should launch a NEW instance.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: updatedUserTask.Namespace, Name: updatedUserTask.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			// A new instance should have been created. The user task will be requeued while it starts.
			Expect(cloudImpl.Instances).Should(HaveLen(2)) // The old failed one plus the new one
			finalUserTask := getUserTaskRun(ctx, client, "test-dyn-pool-fail-1")
			// It won't be assigned a host yet, because the new instance is "starting up"
			Expect(finalUserTask.Labels[AssignedHost]).Should(BeEmpty())
		})

		// It tests the scenario where the pool is at maximum capacity and all
		// available instances fail provisioning for a single TaskRun. The test
		// verifies that the TaskRun will correctly be marked as failed after
		// exhausting all options in the pool.
		It("should fail the task run if all instances fail and the pool is full", func(ctx SpecContext) {
			// Fill the pool to its maximum capacity (2 instances)
			_, err := cloudImpl.LaunchInstance(nil, ctx, "default:preexisting-1", "multi-platform-controller", nil)
			Expect(err).ShouldNot(HaveOccurred())
			_, err = cloudImpl.LaunchInstance(nil, ctx, "default:preexisting-2", "multi-platform-controller", nil)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(cloudImpl.Instances).Should(HaveLen(2))

			// Start a user task. It will be assigned one of the instances.
			userTask := runUserPipeline(ctx, client, reconciler, "test-dyn-pool-all-fail")
			host1 := userTask.Labels[AssignedHost]

			// Fail the first provision task
			provision1 := getProvisionTaskRun(ctx, client, userTask)
			provision1.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provision1.Status.SetCondition(&apis.Condition{Type: apis.ConditionSucceeded, Status: v1.ConditionFalse})
			Expect(client.Status().Update(ctx, provision1)).Should(Succeed())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision1.Namespace, Name: provision1.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(client.Delete(ctx, provision1)).Should(Succeed())

			// Reconcile the user task. It should pick the second available instance.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userTask.Namespace, Name: userTask.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			userTask = getUserTaskRun(ctx, client, "test-dyn-pool-all-fail")
			host2 := userTask.Labels[AssignedHost]
			Expect(host2).ShouldNot(Equal(host1))

			// Fail the second provision task
			provision2 := getProvisionTaskRun(ctx, client, userTask)
			provision2.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provision2.Status.SetCondition(&apis.Condition{Type: apis.ConditionSucceeded, Status: v1.ConditionFalse})
			Expect(client.Status().Update(ctx, provision2)).Should(Succeed())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision2.Namespace, Name: provision2.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			// The task should now have failed both hosts and, since the pool is full, it should give up.
			userTask = getUserTaskRun(ctx, client, "test-dyn-pool-all-fail")
			Expect(userTask.Annotations[FailedHosts]).Should(ContainSubstring(host1))
			Expect(userTask.Annotations[FailedHosts]).Should(ContainSubstring(host2))

			// The final reconcile should result in an error and an error secret.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userTask.Namespace, Name: userTask.Name}})
			Expect(err).Should(HaveOccurred())
			secret := getSecret(ctx, client, userTask)
			Expect(secret.Data["error"]).ShouldNot(BeEmpty())
		})
	})

	// Tests for buildDynamicHostPool function
	When("testing buildDynamicHostPool error paths", func() {
		It("should use default instance tag when platform config doesn't specify one", func(ctx SpecContext) {
			cm := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      HostConfig,
					Namespace: systemNamespace,
					Labels:    map[string]string{ConfigMapLabel: "hosts"},
				},
				Data: map[string]string{
					"instance-tag":                      "global-pool-tag",
					"dynamic-pool-platforms":            "linux/arm64",
					"dynamic.linux-arm64.type":          "aws",
					"dynamic.linux-arm64.max-instances": "2",
					"dynamic.linux-arm64.ssh-secret":    "arm64-secret",
					"dynamic.linux-arm64.concurrency":   "1",
					"dynamic.linux-arm64.max-age":       "20",
					// Note: NO instance-tag field for this platform
				},
			}
			sec := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "arm64-secret",
					Namespace: systemNamespace,
					Labels:    map[string]string{MultiPlatformSecretLabel: "true"},
				},
			}

			_, reconciler := setupClientAndReconciler([]runtimeclient.Object{cm, sec})
			configIface, err := reconciler.getPlatformConfig(ctx, "linux/arm64", userNamespace)
			Expect(err).ShouldNot(HaveOccurred())
			config := configIface.(DynamicHostPool)
			Expect(config.instanceTag).Should(Equal("global-pool-tag"))
		})

		It("should use empty string when neither platform nor default instance tag is specified", func(ctx SpecContext) {
			cm := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      HostConfig,
					Namespace: systemNamespace,
					Labels:    map[string]string{ConfigMapLabel: "hosts"},
				},
				Data: map[string]string{
					"dynamic-pool-platforms":            "linux/arm64",
					"dynamic.linux-arm64.type":          "aws",
					"dynamic.linux-arm64.max-instances": "2",
					"dynamic.linux-arm64.ssh-secret":    "arm64-secret",
					"dynamic.linux-arm64.concurrency":   "1",
					"dynamic.linux-arm64.max-age":       "20",
					// Note: NO instance-tag field for this platform AND no global default
				},
			}
			sec := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "arm64-secret",
					Namespace: systemNamespace,
					Labels:    map[string]string{MultiPlatformSecretLabel: "true"},
				},
			}

			_, reconciler := setupClientAndReconciler([]runtimeclient.Object{cm, sec})
			configIface, err := reconciler.getPlatformConfig(ctx, "linux/arm64", userNamespace)
			Expect(err).ShouldNot(HaveOccurred())
			config := configIface.(DynamicHostPool)
			Expect(config.instanceTag).Should(BeEmpty())
		})
	})
})
