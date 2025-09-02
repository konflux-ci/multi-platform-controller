// This file contains tests focused exclusively on the static host
// provisioning logic. It validates the allocation of hosts from a predefined
// pool, concurrency management, and failure handling for these static hosts.
package taskrun

import (
	"fmt"
	"time"

	mpcmetrics "github.com/konflux-ci/multi-platform-controller/pkg/metrics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Test Static Host Provisioning", func() {

	var client runtimeclient.Client
	var reconciler *ReconcileTaskRun

	BeforeEach(func() {
		client, reconciler = setupClientAndReconciler(createHostConfig())
	})

	// It tests the basic happy-path of allocating a host from the static pool
	// and verifies that the created provisioner TaskRun has the correct parameters.
	It("should allocate a host correctly", func(ctx SpecContext) {
		tr := runUserPipeline(ctx, client, reconciler, "test-static-alloc")
		provision := getProvisionTaskRun(ctx, client, tr)
		params := map[string]string{}
		for _, i := range provision.Spec.Params {
			params[i.Name] = i.Value.StringVal
		}
		Expect(params["SECRET_NAME"]).Should(Equal("multi-platform-ssh-test-static-alloc"))
		Expect(params["TASKRUN_NAME"]).Should(Equal("test-static-alloc"))
		Expect(params["NAMESPACE"]).Should(Equal(userNamespace))
		Expect(params["USER"]).Should(Equal("ec2-user"))
		Expect(params["HOST"]).Should(BeElementOf("ec2-09-876-543-210.compute-1.amazonaws.com", "ec2-12-345-67-890.compute-1.amazonaws.com"))
	})

	// It tests the scenario where all available host slots are occupied.
	// The test ensures that a new TaskRun will wait (indicated by the
	// 'WaitingForPlatformLabel') until a slot is freed up by a completed
	// TaskRun, at which point it gets scheduled correctly.
	It("should wait for concurrency slots to open up", func(ctx SpecContext) {
		// Saturate the host pool by running tasks until all concurrency slots are used.
		runs := []*pipelinev1.TaskRun{}
		for i := 0; i < 8; i++ {
			tr := runUserPipeline(ctx, client, reconciler, fmt.Sprintf("test-%d", i))
			provision := getProvisionTaskRun(ctx, client, tr)
			runSuccessfulProvision(ctx, provision, client, tr, reconciler)
			runs = append(runs, tr)
		}
		// Create one more TaskRun, which should now be forced to wait.
		name := fmt.Sprintf("test-%d", 9)
		createUserTaskRun(ctx, client, name, "linux/arm64")
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
		Expect(err).ShouldNot(HaveOccurred())
		tr := getUserTaskRun(ctx, client, name)
		Expect(tr.Labels[WaitingForPlatformLabel]).Should(Equal("linux-arm64"))
		metricDto := &dto.Metric{}
		var pmetrics *mpcmetrics.PlatformMetrics
		mpcmetrics.HandleMetrics(tr.Labels[TargetPlatformLabel], func(metrics *mpcmetrics.PlatformMetrics) {
			pmetrics = metrics
		})
		Expect(pmetrics).ShouldNot(BeNil())
		gauge, err := pmetrics.WaitingTasks.GetMetricWithLabelValues(tr.Namespace)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(gauge.Write(metricDto)).ShouldNot(HaveOccurred())
		Expect(metricDto.GetGauge().GetValue()).Should(Equal(1.0))
		// Complete one of the running tasks to free up a slot.
		running := runs[0]
		running.Status.CompletionTime = &metav1.Time{Time: time.Now()}
		running.Status.SetCondition(&apis.Condition{
			Type:               apis.ConditionSucceeded,
			Status:             "True",
			LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
		})
		Expect(client.Status().Update(ctx, running)).ShouldNot(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: running.Namespace, Name: running.Name}})
		Expect(err).ShouldNot(HaveOccurred())
		assertNoSecret(ctx, client, running)

		// Verify that the waiting TaskRun is now allocated a host.
		tr = getUserTaskRun(ctx, client, name)
		Expect(tr.Labels[FinishedWaitingLabel]).Should(Equal("true"))
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
		Expect(err).ShouldNot(HaveOccurred())
		tr = getUserTaskRun(ctx, client, name)
		Expect(getProvisionTaskRun(ctx, client, tr)).ShouldNot(BeNil())
		Expect(tr.Labels[AssignedHost]).ShouldNot(BeEmpty())

		mpcmetrics.HandleMetrics(tr.Labels[TargetPlatformLabel], func(metrics *mpcmetrics.PlatformMetrics) {
			pmetrics = metrics
		})
		Expect(pmetrics).ShouldNot(BeNil())
		gauge, err = pmetrics.WaitingTasks.GetMetricWithLabelValues(tr.Namespace)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(gauge.Write(metricDto)).ShouldNot(HaveOccurred())
		Expect(metricDto.GetGauge().GetValue()).Should(Equal(0.0))
	})

	When("when provisioning fails", func() {

		// It tests the reallocation logic. When a provisioner TaskRun fails for
		// an assigned host, the test ensures that the host is marked as failed
		// (in the 'FailedHosts' annotation) and that the system attempts to
		// allocate a different host from the pool.
		It("should mark the host as failed and attempt to re-allocate", func(ctx SpecContext) {
			// Create the initial user task that needs a host.
			userTask := runUserPipeline(ctx, client, reconciler, "test-single-failure")
			Expect(userTask.Labels[AssignedHost]).NotTo(BeEmpty(), "A host should have been assigned initially")
			initialHost := userTask.Labels[AssignedHost]

			// Get the provision task that was created for our user task.
			provisionTask := getProvisionTaskRun(ctx, client, userTask)
			Expect(provisionTask).NotTo(BeNil(), "A provision task should have been created")

			// Telling the system "this thing failed."
			provisionTask.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provisionTask.Status.SetCondition(&apis.Condition{
				Type:   apis.ConditionSucceeded,
				Status: v1.ConditionFalse, // Here's the failure!
			})
			Expect(client.Status().Update(ctx, provisionTask)).Should(Succeed(), "Failed to update provision task to a failed state")

			// Failure and update the original user task.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provisionTask.Namespace, Name: provisionTask.Name}})
			Expect(err).NotTo(HaveOccurred(), "Reconciling the failed provision task should not error")

			Expect(client.Delete(ctx, provisionTask)).Should(Succeed(), "Failed to delete the old provision task")

			// Get the user task again and see what state it's in.
			updatedUserTask := getUserTaskRun(ctx, client, "test-single-failure")
			Expect(updatedUserTask.Annotations[FailedHosts]).Should(ContainSubstring(initialHost), "The failed host should be recorded")
			Expect(updatedUserTask.Labels[AssignedHost]).Should(BeEmpty(), "The failed host should be un-assigned")

			// The reconciler should try to find a NEW host.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: updatedUserTask.Namespace, Name: updatedUserTask.Name}})
			Expect(err).NotTo(HaveOccurred(), "Re-reconciling the user task should not error")

			// Verify that a new host was found.
			finalUserTask := getUserTaskRun(ctx, client, "test-single-failure")
			Expect(finalUserTask.Labels[AssignedHost]).NotTo(BeEmpty(), "A new host should have been assigned")
			Expect(finalUserTask.Labels[AssignedHost]).NotTo(Equal(initialHost), "The new host should not be the same as the one that failed")

			// We should have a new provision task.
			newProvisionTask := getProvisionTaskRun(ctx, client, finalUserTask)
			Expect(newProvisionTask).NotTo(BeNil(), "A new provision task should have been created for the new host")
			Expect(newProvisionTask.UID).NotTo(Equal(provisionTask.UID), "The new provision task should have a different UID, indicating it's a new object")
		})

		// It tests the scenario where every available host in the static pool
		// fails provisioning. The test ensures that after all hosts have been
		// tried and failed, the user TaskRun is ultimately marked as failed
		// and an error is written to its secret.
		It("should fail the task run after all hosts have been tried", func(ctx SpecContext) {
			tr := runUserPipeline(ctx, client, reconciler, "test-all-fail")
			provision1 := getProvisionTaskRun(ctx, client, tr)
			host1 := provision1.Labels[AssignedHost]

			// Fail the first host
			provision1.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provision1.Status.SetCondition(&apis.Condition{
				Type:   apis.ConditionSucceeded,
				Status: v1.ConditionFalse,
			})
			Expect(client.Status().Update(ctx, provision1)).ShouldNot(HaveOccurred())
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision1.Namespace, Name: provision1.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(client.Delete(ctx, provision1)).Should(Succeed())

			// Reconcile the user task to try the next host
			tr = getUserTaskRun(ctx, client, "test-all-fail")
			Expect(tr.Annotations[FailedHosts]).Should(ContainSubstring(host1))
			Expect(tr.Labels[AssignedHost]).Should(BeEmpty())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			// Fail the second host
			provision2 := getProvisionTaskRun(ctx, client, tr)
			host2 := provision2.Labels[AssignedHost]
			provision2.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provision2.Status.SetCondition(&apis.Condition{
				Type:   apis.ConditionSucceeded,
				Status: v1.ConditionFalse,
			})
			Expect(client.Status().Update(ctx, provision2)).ShouldNot(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision2.Namespace, Name: provision2.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			// Check final state
			tr = getUserTaskRun(ctx, client, "test-all-fail")
			Expect(tr.Annotations[FailedHosts]).Should(ContainSubstring(host1))
			Expect(tr.Annotations[FailedHosts]).Should(ContainSubstring(host2))
			Expect(tr.Labels[AssignedHost]).Should(BeEmpty())

			// Final reconcile should now fail the task run
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).Should(HaveOccurred())

			secret := getSecret(ctx, client, tr)
			Expect(secret.Data["error"]).ShouldNot(BeEmpty())
		})
	})

	When("when provisioning succeeds", func() {

		// It tests a specific failure case where the provisioner TaskRun reports
		// success, but the secret containing the SSH key and host information is
		// never created. The test ensures the controller handles this by creating
		// an error secret for the user TaskRun.
		It("should create an error secret if the provision task succeeds but does not create a secret", func(ctx SpecContext) {
			tr := runUserPipeline(ctx, client, reconciler, "test-no-secret")
			provision := getProvisionTaskRun(ctx, client, tr)

			provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provision.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			Expect(client.Status().Update(ctx, provision)).ShouldNot(HaveOccurred())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			secret := getSecret(ctx, client, tr)
			Expect(secret.Data["error"]).ShouldNot(BeEmpty())
		})

		// It tests the end-to-end happy path, including cleanup.
		// A user TaskRun is created, provisioned, completes successfully, and
		// the test verifies that all related resources (secrets, provisioner TaskRuns)
		// are properly deleted afterward.
		It("should successfully provision and clean up", func(ctx SpecContext) {
			tr := runUserPipeline(ctx, client, reconciler, "test-success")
			provision := getProvisionTaskRun(ctx, client, tr)

			runSuccessfulProvision(ctx, provision, client, tr, reconciler)

			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:               apis.ConditionSucceeded,
				Status:             "True",
				LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now()}},
			})
			Expect(client.Status().Update(ctx, tr)).ShouldNot(HaveOccurred())
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			assertNoSecret(ctx, client, tr)

			list := pipelinev1.TaskRunList{}
			err = client.List(ctx, &list)
			Expect(err).ShouldNot(HaveOccurred())

			for idx := range list.Items {
				i := list.Items[idx]
				if i.Labels[TaskTypeLabel] != "" {
					if i.Status.CompletionTime == nil {
						endTime := time.Now().Add(time.Hour * -2)
						i.Status.CompletionTime = &metav1.Time{Time: endTime}
						i.Status.SetCondition(&apis.Condition{
							Type:               apis.ConditionSucceeded,
							Status:             "True",
							LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: endTime}},
						})
						Expect(client.Status().Update(ctx, &i)).ShouldNot(HaveOccurred())
					}

					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: i.Namespace, Name: i.Name}})
					Expect(err).ShouldNot(HaveOccurred())
				}
			}

			taskExists := false
			err = client.List(ctx, &list)
			Expect(err).ShouldNot(HaveOccurred())
			for _, i := range list.Items {
				if i.Labels[TaskTypeLabel] != "" {
					taskExists = true
				}
			}
			Expect(taskExists).Should(BeFalse())
		})
		It("should increment Provision Successes metric when provision task succeeds", func(ctx SpecContext) {
			// Get initial metric value
			initialSuccesses := getCounterValue("linux/arm64", "provisioning_successes")

			tr := runUserPipeline(ctx, client, reconciler, "test-success")
			provision := getProvisionTaskRun(ctx, client, tr)

			runSuccessfulProvision(ctx, provision, client, tr, reconciler)

			// Verify the Provision Successes metric incremented
			Expect(getCounterValue("linux/arm64", "provisioning_successes")).Should(Equal(initialSuccesses + 1))
		})

		It("should increment Provision Successes metric by one when provision task succeeds after a conflict", func(ctx SpecContext) {
			// Get initial metric value
			initialSuccesses := getCounterValue("linux/arm64", "provisioning_successes")

			// Run normal provision setup
			tr := runUserPipeline(ctx, client, reconciler, "test-success-race")
			provision := getProvisionTaskRun(ctx, client, tr)

			// Run successful provision with conflict - this will simulate the race condition between the MPC and Tekton
			runSuccessfulProvisionWithConflict(ctx, provision, client, tr, reconciler)

			// Verify the Provision Successes metric incremented only by one despite the conflict
			Expect(getCounterValue("linux/arm64", "provisioning_successes")).Should(Equal(initialSuccesses + 1))

			// Verify both external and MPC changes are preserved after conflict resolution
			updated := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, updated)).Should(Succeed())
			Expect(updated.Labels).Should(HaveKeyWithValue("external-label", "external-value"))
			Expect(updated.Annotations).Should(HaveKeyWithValue("external-annotation", "external-value"))
			Expect(updated.Finalizers).Should(ContainElement("external-finalizer"))
		})
	})
})
