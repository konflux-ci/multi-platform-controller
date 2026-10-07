// This file contains tests focused exclusively on the static host
// provisioning logic. It validates the allocation of hosts from a predefined
// pool, concurrency management, and failure handling for these static hosts.
package taskrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr/funcr"
	. "github.com/konflux-ci/multi-platform-controller/pkg/constant"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	ctrl "sigs.k8s.io/controller-runtime"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
		Expect(params["HOST"]).Should(BeElementOf("192.0.2.1", "192.0.2.2"))
		Expect(provision.Spec.Workspaces).Should(HaveLen(1))
		Expect(provision.Spec.Workspaces[0].Name).Should(Equal("ssh"))
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
			// run a user task successfully
			tr := runUserPipeline(ctx, client, reconciler, "test-success")
			// Get initial metric value
			initialSuccesses := getCounterValue("linux/arm64", "provisioning_successes")
			Expect(initialSuccesses).ShouldNot(Equal(-1.0))

			provision := getProvisionTaskRun(ctx, client, tr)

			runSuccessfulProvision(ctx, provision, client, tr, reconciler)

			// Verify the Provision Successes metric incremented
			Expect(getCounterValue("linux/arm64", "provisioning_successes")).Should(Equal(initialSuccesses + 1))
		})

		It("should increment Provision Successes metric by one when provision task succeeds after a conflict", func(ctx SpecContext) {
			// run a user task with a conflict
			tr := runUserPipeline(ctx, client, reconciler, "test-success-race")
			// Get initial metric value
			initialSuccesses := getCounterValue("linux/arm64", "provisioning_successes")
			Expect(initialSuccesses).ShouldNot(Equal(-1.0))
			// Run normal provision setup
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

	When("a static host sets ssh-config", func() {
		const sshConfigText = "Host *\n  ProxyJump bastion.example.com"

		BeforeEach(func() {
			client, reconciler = setupClientAndReconciler(staticHostsWithSSHConfig(sshConfigText))
		})

		It("should mount that text on the provision task", func(ctx SpecContext) {
			tr := runUserPipeline(ctx, client, reconciler, "test-ssh-config")
			provision := getProvisionTaskRun(ctx, client, tr)
			binding := sshConfigBinding(provision)
			expectSSHConfigSnapshot(ctx, client, tr, binding, sshConfigText)

			hostConfig := &v1.ConfigMap{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: HostConfig}, hostConfig)).Should(Succeed())
			delete(hostConfig.Data, "host."+tr.Labels[AssignedHost]+".ssh-config")
			Expect(client.Update(ctx, hostConfig)).Should(Succeed())

			snapshot := &v1.ConfigMap{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: binding.ConfigMap.Name}, snapshot)).Should(Succeed())
			Expect(snapshot.Data).Should(HaveKeyWithValue(sshConfigFileName, sshConfigText))
		})

		It("should mount the same text on the cleanup task", func(ctx SpecContext) {
			tr := runUserPipeline(ctx, client, reconciler, "test-ssh-config-cleanup")
			provisionBinding := sshConfigBinding(getProvisionTaskRun(ctx, client, tr))

			hostConfig := &v1.ConfigMap{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: HostConfig}, hostConfig)).Should(Succeed())
			hostConfig.Data["host."+tr.Labels[AssignedHost]+".ssh-config"] = "Host *\n  ProxyJump other.example.com"
			Expect(client.Update(ctx, hostConfig)).Should(Succeed())

			tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			tr.Status.SetCondition(&apis.Condition{
				Type:   apis.ConditionSucceeded,
				Status: v1.ConditionTrue,
			})
			Expect(client.Status().Update(ctx, tr)).Should(Succeed())
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			cleanupTasks := &pipelinev1.TaskRunList{}
			Expect(client.List(ctx, cleanupTasks, runtimeclient.MatchingLabels{
				TaskTypeLabel:     TaskTypeClean,
				UserTaskName:      tr.Name,
				UserTaskNamespace: userNamespace,
			})).Should(Succeed())
			Expect(cleanupTasks.Items).Should(HaveLen(1))
			binding := sshConfigBinding(&cleanupTasks.Items[0])
			Expect(binding.ConfigMap.Name).Should(Equal(provisionBinding.ConfigMap.Name))
			expectSSHConfigSnapshot(ctx, client, tr, binding, sshConfigText)

			cleanup := &cleanupTasks.Items[0]
			cleanup.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			cleanup.Status.SetCondition(&apis.Condition{
				Type:   apis.ConditionSucceeded,
				Status: v1.ConditionTrue,
			})
			Expect(client.Status().Update(ctx, cleanup)).Should(Succeed())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: cleanup.Namespace, Name: cleanup.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			err = client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: binding.ConfigMap.Name}, &v1.ConfigMap{})
			Expect(k8serrors.IsNotFound(err)).Should(BeTrue())
		})

		It("should replace a stale ssh-config snapshot", func(ctx SpecContext) {
			name := "test-stale-snapshot"
			snapshotName := sshConfigSnapshotName(userNamespace, name)
			stale := &v1.ConfigMap{}
			stale.Name = snapshotName
			stale.Namespace = systemNamespace
			immutable := true
			stale.Immutable = &immutable
			stale.Data = map[string]string{sshConfigFileName: "Host *\n  ProxyJump old.example.com"}
			Expect(client.Create(ctx, stale)).Should(Succeed())

			created, err := createSSHConfigMap(ctx, client, client, systemNamespace, snapshotName, sshConfigText, "")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(created.Data).Should(HaveKeyWithValue(sshConfigFileName, sshConfigText))

			stored := &v1.ConfigMap{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, stored)).Should(Succeed())
			Expect(stored.Data).Should(HaveKeyWithValue(sshConfigFileName, sshConfigText))
			Expect(stored.Immutable).ShouldNot(BeNil())
			Expect(*stored.Immutable).Should(BeTrue())
		})

		It("should return the snapshot delete error when a stale snapshot cannot be replaced", func(ctx SpecContext) {
			snapshotName := sshConfigSnapshotName(userNamespace, "test-stale-snapshot-delete")
			stale := &v1.ConfigMap{}
			stale.Name = snapshotName
			stale.Namespace = systemNamespace
			immutable := true
			stale.Immutable = &immutable
			stale.Data = map[string]string{sshConfigFileName: "Host *\n  ProxyJump old.example.com"}
			Expect(client.Create(ctx, stale)).Should(Succeed())

			wrapped := failSnapshotDelete{failTaskRunUpdate: failTaskRunUpdate{Client: client}, snapshotName: snapshotName}
			_, err := createSSHConfigMap(ctx, wrapped, client, systemNamespace, snapshotName, sshConfigText, "")
			Expect(err).Should(MatchError(ContainSubstring("delete snapshot failed")))

			stored := &v1.ConfigMap{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, stored)).Should(Succeed())
			Expect(stored.Data).Should(HaveKeyWithValue(sshConfigFileName, "Host *\n  ProxyJump old.example.com"))
		})

		It("should keep an existing snapshot when the ssh-config text matches", func(ctx SpecContext) {
			snapshotName := sshConfigSnapshotName(userNamespace, "test-matching-snapshot")
			existing := &v1.ConfigMap{}
			existing.Name = snapshotName
			existing.Namespace = systemNamespace
			immutable := true
			existing.Immutable = &immutable
			existing.Data = map[string]string{sshConfigFileName: sshConfigText}
			Expect(client.Create(ctx, existing)).Should(Succeed())

			created, err := createSSHConfigMap(ctx, client, client, systemNamespace, snapshotName, sshConfigText, userNamespace+"/test-matching-snapshot")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(created.UID).Should(Equal(existing.UID))
			Expect(created.Data).Should(HaveKeyWithValue(sshConfigFileName, sshConfigText))
		})

		It("should return a lookup error when an existing snapshot cannot be read", func(ctx SpecContext) {
			wrapped := alreadyExistsMissing{Client: client}
			_, err := createSSHConfigMap(ctx, wrapped, wrapped, systemNamespace, "ssh-config-missing", sshConfigText, "")
			Expect(k8serrors.IsNotFound(err)).Should(BeTrue())
		})

		It("should not replace a snapshot recorded for another TaskRun", func(ctx SpecContext) {
			snapshotName := sshConfigSnapshotName(userNamespace, "test-other-snapshot")
			stale := &v1.ConfigMap{}
			stale.Name = snapshotName
			stale.Namespace = systemNamespace
			immutable := true
			stale.Immutable = &immutable
			stale.Data = map[string]string{sshConfigFileName: "Host *\n  ProxyJump old.example.com"}
			stale.Annotations = map[string]string{sshConfigSourceAnnotation: "other-ns/test-other-snapshot"}
			Expect(client.Create(ctx, stale)).Should(Succeed())

			_, err := createSSHConfigMap(ctx, client, client, systemNamespace, snapshotName, sshConfigText, userNamespace+"/test-other-snapshot")
			Expect(err).Should(MatchError(ContainSubstring("already exists with different contents")))

			stored := &v1.ConfigMap{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, stored)).Should(Succeed())
			Expect(stored.Data).Should(HaveKeyWithValue(sshConfigFileName, "Host *\n  ProxyJump old.example.com"))
		})

		It("should return an error when a replaced snapshot still has different content", func(ctx SpecContext) {
			snapshotName := sshConfigSnapshotName(userNamespace, "test-stale-snapshot-again")
			wrapped := mismatchAfterReplace{Client: client}
			_, err := createSSHConfigMap(ctx, wrapped, wrapped, systemNamespace, snapshotName, sshConfigText, "")
			Expect(err).Should(MatchError(ContainSubstring("already exists with different contents")))
		})

		It("should snapshot the next host when the first provision fails", func(ctx SpecContext) {
			const otherSSHConfig = "Host *\n  ProxyJump other.example.com"
			client, reconciler = setupClientAndReconciler(staticHostsWithDistinctSSHConfig(sshConfigText, otherSSHConfig))

			userTask := runUserPipeline(ctx, client, reconciler, "test-ssh-config-retry")
			initialHost := userTask.Labels[AssignedHost]
			provisionTask := getProvisionTaskRun(ctx, client, userTask)
			provisionTask.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			provisionTask.Status.SetCondition(&apis.Condition{
				Type:   apis.ConditionSucceeded,
				Status: v1.ConditionFalse,
			})
			Expect(client.Status().Update(ctx, provisionTask)).Should(Succeed())
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provisionTask.Namespace, Name: provisionTask.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(client.Delete(ctx, provisionTask)).Should(Succeed())

			updatedUserTask := getUserTaskRun(ctx, client, userTask.Name)
			Expect(updatedUserTask.Annotations[sshConfigSnapshotAnnotation]).Should(BeEmpty())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: userTask.Name}})
			Expect(err).ShouldNot(HaveOccurred())

			finalUserTask := getUserTaskRun(ctx, client, userTask.Name)
			Expect(finalUserTask.Labels[AssignedHost]).ShouldNot(Equal(initialHost))
			nextProvision := getProvisionTaskRun(ctx, client, finalUserTask)
			hostConfig := &v1.ConfigMap{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: HostConfig}, hostConfig)).Should(Succeed())
			expectSSHConfigSnapshot(ctx, client, finalUserTask, sshConfigBinding(nextProvision), hostConfig.Data["host."+finalUserTask.Labels[AssignedHost]+".ssh-config"])
		})

		It("should delete the snapshot when the user TaskRun update fails", func(ctx SpecContext) {
			name := "test-ssh-config-update-failure"
			createUserTaskRun(ctx, client, name, "linux/arm64")
			reconciler.client = failTaskRunUpdate{Client: client}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
			Expect(err).Should(HaveOccurred())

			snapshotErr := client.Get(ctx, types.NamespacedName{
				Namespace: systemNamespace,
				Name:      sshConfigSnapshotName(userNamespace, name),
			}, &v1.ConfigMap{})
			Expect(k8serrors.IsNotFound(snapshotErr)).Should(BeTrue())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: HostConfig}, &v1.ConfigMap{})).Should(Succeed())
		})

		It("should log a snapshot delete failure when the user TaskRun update fails", func(ctx SpecContext) {
			name := "test-ssh-config-delete-failure"
			createUserTaskRun(ctx, client, name, "linux/arm64")
			snapshotName := sshConfigSnapshotName(userNamespace, name)
			reconciler.client = failSnapshotDelete{failTaskRunUpdate: failTaskRunUpdate{Client: client}, snapshotName: snapshotName}

			previousLog := ctrl.Log
			var logged []string
			ctrl.Log = funcr.New(func(_, args string) {
				logged = append(logged, args)
			}, funcr.Options{})
			defer func() { ctrl.Log = previousLog }()

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
			Expect(err).Should(MatchError(ContainSubstring("induced update failure")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, &v1.ConfigMap{})).Should(Succeed())
			Expect(logged).Should(ContainElement(And(
				ContainSubstring("failed to delete ssh-config snapshot"),
				ContainSubstring("delete snapshot failed"),
			)))

			Expect(reconciler.client.Delete(ctx, &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace, Name: HostConfig}})).Should(Succeed())
			err = client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: HostConfig}, &v1.ConfigMap{})
			Expect(k8serrors.IsNotFound(err)).Should(BeTrue())
		})
	})

	It("should ignore a snapshot annotation that names another ConfigMap", func(ctx SpecContext) {
		client, reconciler = setupClientAndReconciler(createHostConfig())
		name := "test-forged-snapshot"
		createUserTaskRun(ctx, client, name, "linux/arm64")
		userTask := getUserTaskRun(ctx, client, name)
		userTask.Annotations = map[string]string{sshConfigSnapshotAnnotation: HostConfig}
		Expect(client.Update(ctx, userTask)).Should(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
		Expect(err).ShouldNot(HaveOccurred())
		provision := getProvisionTaskRun(ctx, client, getUserTaskRun(ctx, client, name))
		for _, binding := range provision.Spec.Workspaces {
			if binding.ConfigMap != nil {
				Expect(binding.ConfigMap.Name).ShouldNot(Equal(HostConfig))
			}
		}

		provision.Status.CompletionTime = &metav1.Time{Time: time.Now()}
		provision.Status.SetCondition(&apis.Condition{
			Type:   apis.ConditionSucceeded,
			Status: v1.ConditionFalse,
		})
		Expect(client.Status().Update(ctx, provision)).Should(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
		Expect(err).ShouldNot(HaveOccurred())
		Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: HostConfig}, &v1.ConfigMap{})).Should(Succeed())
	})

	When("an update task finishes", func() {
		const snapshotName = "ssh-config-update"

		createFinishedUpdate := func(ctx SpecContext, name string, completed time.Time, succeeded v1.ConditionStatus) {
			snapshot := &v1.ConfigMap{}
			snapshot.Name = snapshotName
			snapshot.Namespace = systemNamespace
			snapshot.Data = map[string]string{sshConfigFileName: "Host *\n  ProxyJump bastion"}
			Expect(client.Create(ctx, snapshot)).Should(Succeed())

			tr := &pipelinev1.TaskRun{}
			tr.Name = name
			tr.Namespace = systemNamespace
			tr.Labels = map[string]string{TaskTypeLabel: TaskTypeUpdate}
			tr.Spec.Workspaces = []pipelinev1.WorkspaceBinding{
				{Name: "ssh", Secret: &v1.SecretVolumeSource{SecretName: "host-key"}},
				sshConfigWorkspaceBinding(snapshotName),
			}
			Expect(client.Create(ctx, tr)).Should(Succeed())

			stored := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: name}, stored)).Should(Succeed())
			stored.Status.CompletionTime = &metav1.Time{Time: completed}
			stored.Status.SetCondition(&apis.Condition{Type: apis.ConditionSucceeded, Status: succeeded})
			Expect(client.Status().Update(ctx, stored)).Should(Succeed())

			snap := &v1.ConfigMap{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, snap)).Should(Succeed())
			Expect(controllerutil.SetOwnerReference(stored, snap, reconciler.scheme)).Should(Succeed())
			Expect(client.Update(ctx, snap)).Should(Succeed())
		}

		It("should delete a succeeded update task and its ssh-config snapshot", func(ctx SpecContext) {
			createFinishedUpdate(ctx, "update-succeeded", time.Now(), v1.ConditionTrue)
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: "update-succeeded"}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(k8serrors.IsNotFound(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: "update-succeeded"}, &pipelinev1.TaskRun{}))).Should(BeTrue())
			Expect(k8serrors.IsNotFound(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, &v1.ConfigMap{}))).Should(BeTrue())
		})

		It("should keep a running update task and its snapshot", func(ctx SpecContext) {
			snapshot := &v1.ConfigMap{}
			snapshot.Name = snapshotName
			snapshot.Namespace = systemNamespace
			snapshot.Data = map[string]string{sshConfigFileName: "Host *\n  ProxyJump bastion"}
			Expect(client.Create(ctx, snapshot)).Should(Succeed())

			tr := &pipelinev1.TaskRun{}
			tr.Name = "update-running"
			tr.Namespace = systemNamespace
			tr.Labels = map[string]string{TaskTypeLabel: TaskTypeUpdate}
			tr.Spec.Workspaces = []pipelinev1.WorkspaceBinding{
				{Name: "ssh", Secret: &v1.SecretVolumeSource{SecretName: "host-key"}},
				sshConfigWorkspaceBinding(snapshotName),
			}
			Expect(client.Create(ctx, tr)).Should(Succeed())

			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(result.RequeueAfter).Should(BeZero())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: tr.Name}, &pipelinev1.TaskRun{})).Should(Succeed())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, &v1.ConfigMap{})).Should(Succeed())
		})

		It("should keep a recently failed update task", func(ctx SpecContext) {
			createFinishedUpdate(ctx, "update-failed-recent", time.Now(), v1.ConditionFalse)
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: "update-failed-recent"}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(result.RequeueAfter).Should(Equal(time.Hour))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: "update-failed-recent"}, &pipelinev1.TaskRun{})).Should(Succeed())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, &v1.ConfigMap{})).Should(Succeed())
		})

		It("should delete an old failed update task and its snapshot", func(ctx SpecContext) {
			createFinishedUpdate(ctx, "update-failed-old", time.Now().Add(-2*time.Hour), v1.ConditionFalse)
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: "update-failed-old"}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(k8serrors.IsNotFound(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: "update-failed-old"}, &pipelinev1.TaskRun{}))).Should(BeTrue())
			Expect(k8serrors.IsNotFound(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, &v1.ConfigMap{}))).Should(BeTrue())
		})

		It("should leave the snapshot when the update task is being deleted", func(ctx SpecContext) {
			snapshot := &v1.ConfigMap{}
			snapshot.Name = snapshotName
			snapshot.Namespace = systemNamespace
			snapshot.Data = map[string]string{sshConfigFileName: "Host *\n  ProxyJump bastion"}
			Expect(client.Create(ctx, snapshot)).Should(Succeed())

			tr := &pipelinev1.TaskRun{}
			tr.Name = "update-deleting"
			tr.Namespace = systemNamespace
			tr.Finalizers = []string{"test.keep"}
			tr.Labels = map[string]string{TaskTypeLabel: TaskTypeUpdate}
			tr.Spec.Workspaces = []pipelinev1.WorkspaceBinding{sshConfigWorkspaceBinding(snapshotName)}
			Expect(client.Create(ctx, tr)).Should(Succeed())

			stored := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: tr.Name}, stored)).Should(Succeed())
			stored.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			stored.Status.SetCondition(&apis.Condition{Type: apis.ConditionSucceeded, Status: v1.ConditionTrue})
			Expect(client.Status().Update(ctx, stored)).Should(Succeed())
			Expect(controllerutil.SetOwnerReference(stored, snapshot, reconciler.scheme)).Should(Succeed())
			Expect(client.Update(ctx, snapshot)).Should(Succeed())
			Expect(client.Delete(ctx, stored)).Should(Succeed())

			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(result.RequeueAfter).Should(BeZero())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, &v1.ConfigMap{})).Should(Succeed())
			deleting := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: tr.Name}, deleting)).Should(Succeed())
			Expect(deleting.DeletionTimestamp.IsZero()).Should(BeFalse())
		})

		It("should not delete host-config for an update task outside the operator namespace", func(ctx SpecContext) {
			tr := &pipelinev1.TaskRun{}
			tr.Name = "user-update"
			tr.Namespace = userNamespace
			tr.Labels = map[string]string{TaskTypeLabel: TaskTypeUpdate}
			tr.Spec.Workspaces = []pipelinev1.WorkspaceBinding{sshConfigWorkspaceBinding(HostConfig)}
			Expect(client.Create(ctx, tr)).Should(Succeed())

			stored := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: userNamespace, Name: tr.Name}, stored)).Should(Succeed())
			stored.Status.CompletionTime = &metav1.Time{Time: time.Now().Add(-2 * time.Hour)}
			stored.Status.SetCondition(&apis.Condition{Type: apis.ConditionSucceeded, Status: v1.ConditionTrue})
			Expect(client.Status().Update(ctx, stored)).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: HostConfig}, &v1.ConfigMap{})).Should(Succeed())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: userNamespace, Name: tr.Name}, &pipelinev1.TaskRun{})).Should(Succeed())
		})

		It("should not delete a ConfigMap the update task does not own", func(ctx SpecContext) {
			tr := &pipelinev1.TaskRun{}
			tr.Name = "update-unowned"
			tr.Namespace = systemNamespace
			tr.Labels = map[string]string{TaskTypeLabel: TaskTypeUpdate}
			tr.Spec.Workspaces = []pipelinev1.WorkspaceBinding{sshConfigWorkspaceBinding(HostConfig)}
			Expect(client.Create(ctx, tr)).Should(Succeed())

			stored := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: tr.Name}, stored)).Should(Succeed())
			stored.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			stored.Status.SetCondition(&apis.Condition{Type: apis.ConditionSucceeded, Status: v1.ConditionTrue})
			Expect(client.Status().Update(ctx, stored)).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: tr.Name}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: HostConfig}, &v1.ConfigMap{})).Should(Succeed())
			Expect(k8serrors.IsNotFound(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: tr.Name}, &pipelinev1.TaskRun{}))).Should(BeTrue())
		})

		It("should delete a finished update task when its snapshot is already gone", func(ctx SpecContext) {
			createFinishedUpdate(ctx, "update-snapshot-gone", time.Now(), v1.ConditionTrue)
			Expect(client.Delete(ctx, &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace, Name: snapshotName}})).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: "update-snapshot-gone"}})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(k8serrors.IsNotFound(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: "update-snapshot-gone"}, &pipelinev1.TaskRun{}))).Should(BeTrue())
		})

		It("should keep the update task when its snapshot cannot be read", func(ctx SpecContext) {
			createFinishedUpdate(ctx, "update-snapshot-unreadable", time.Now(), v1.ConditionTrue)
			reconciler.client = failSnapshotGet{Client: client, name: snapshotName}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: "update-snapshot-unreadable"}})
			Expect(err).Should(MatchError(ContainSubstring("get snapshot failed")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: "update-snapshot-unreadable"}, &pipelinev1.TaskRun{})).Should(Succeed())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, &v1.ConfigMap{})).Should(Succeed())
		})

		It("should keep the update task when deleting its snapshot fails", func(ctx SpecContext) {
			createFinishedUpdate(ctx, "update-snapshot-delete-fails", time.Now(), v1.ConditionTrue)
			reconciler.client = failSnapshotDelete{failTaskRunUpdate: failTaskRunUpdate{Client: client}, snapshotName: snapshotName}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: systemNamespace, Name: "update-snapshot-delete-fails"}})
			Expect(err).Should(MatchError(ContainSubstring("delete snapshot failed")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: "update-snapshot-delete-fails"}, &pipelinev1.TaskRun{})).Should(Succeed())
			Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: snapshotName}, &v1.ConfigMap{})).Should(Succeed())
		})

		It("should recognize only the TaskRun UID as the snapshot owner", func() {
			cm := &v1.ConfigMap{}
			tr := &pipelinev1.TaskRun{}
			Expect(ownedByTaskRun(cm, tr)).Should(BeFalse())

			tr.UID = "task-uid"
			cm.OwnerReferences = []metav1.OwnerReference{{UID: "other-uid"}}
			Expect(ownedByTaskRun(cm, tr)).Should(BeFalse())

			cm.OwnerReferences[0].UID = tr.UID
			Expect(ownedByTaskRun(cm, tr)).Should(BeTrue())
		})
	})
})

func staticHostsWithDistinctSSHConfig(host1Config, host2Config string) []runtimeclient.Object {
	objs := createHostConfig()
	hostConfig := objs[0].(*v1.ConfigMap)
	hostConfig.Data["host.host1.ssh-config"] = host1Config
	hostConfig.Data["host.host2.ssh-config"] = host2Config
	return objs
}

type failTaskRunUpdate struct {
	runtimeclient.Client
}

func (f failTaskRunUpdate) Update(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.UpdateOption) error {
	if _, ok := obj.(*pipelinev1.TaskRun); ok {
		return errors.New("induced update failure")
	}
	return f.Client.Update(ctx, obj, opts...)
}

type failSnapshotDelete struct {
	failTaskRunUpdate
	snapshotName string
}

func (f failSnapshotDelete) Delete(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.DeleteOption) error {
	if cm, ok := obj.(*v1.ConfigMap); ok && cm.Name == f.snapshotName {
		return errors.New("delete snapshot failed")
	}
	return f.Client.Delete(ctx, obj, opts...)
}

type failSnapshotGet struct {
	runtimeclient.Client
	name string
}

func (f failSnapshotGet) Get(ctx context.Context, key runtimeclient.ObjectKey, obj runtimeclient.Object, opts ...runtimeclient.GetOption) error {
	if key.Name == f.name {
		return errors.New("get snapshot failed")
	}
	return f.Client.Get(ctx, key, obj, opts...)
}

// mismatchAfterReplace reports a deterministic ConfigMap as already present with other content,
// including after it is deleted, so a single replacement cannot succeed.
type mismatchAfterReplace struct {
	runtimeclient.Client
}

func (m mismatchAfterReplace) Create(_ context.Context, obj runtimeclient.Object, _ ...runtimeclient.CreateOption) error {
	return k8serrors.NewAlreadyExists(schema.GroupResource{Resource: "configmaps"}, obj.GetName())
}

func (m mismatchAfterReplace) Get(_ context.Context, key runtimeclient.ObjectKey, obj runtimeclient.Object, _ ...runtimeclient.GetOption) error {
	cm := obj.(*v1.ConfigMap)
	cm.Name = key.Name
	cm.Namespace = key.Namespace
	cm.Data = map[string]string{sshConfigFileName: "Host *\n  ProxyJump old.example.com"}
	return nil
}

type alreadyExistsMissing struct {
	runtimeclient.Client
}

func (a alreadyExistsMissing) Create(_ context.Context, obj runtimeclient.Object, _ ...runtimeclient.CreateOption) error {
	return k8serrors.NewAlreadyExists(schema.GroupResource{Resource: "configmaps"}, obj.GetName())
}

func (a alreadyExistsMissing) Get(_ context.Context, key runtimeclient.ObjectKey, _ runtimeclient.Object, _ ...runtimeclient.GetOption) error {
	return k8serrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, key.Name)
}

func (m mismatchAfterReplace) Delete(context.Context, runtimeclient.Object, ...runtimeclient.DeleteOption) error {
	return nil
}

func staticHostsWithSSHConfig(sshConfig string) []runtimeclient.Object {
	objs := createHostConfig()
	hostConfig := objs[0].(*v1.ConfigMap)
	hostConfig.Data["host.host1.ssh-config"] = sshConfig
	hostConfig.Data["host.host2.ssh-config"] = sshConfig
	return objs
}

func expectSSHConfigSnapshot(ctx SpecContext, client runtimeclient.Client, userTask *pipelinev1.TaskRun, binding pipelinev1.WorkspaceBinding, sshConfigText string) {
	Expect(binding.ConfigMap.Name).Should(Equal(userTask.Annotations[sshConfigSnapshotAnnotation]))
	Expect(binding.ConfigMap.Name).ShouldNot(Equal(HostConfig))
	Expect(binding.SubPath).Should(Equal(sshConfigFileName))
	Expect(binding.ConfigMap.Items).Should(HaveLen(1))
	Expect(binding.ConfigMap.Items[0].Key).Should(Equal(sshConfigFileName))
	Expect(binding.ConfigMap.Items[0].Path).Should(Equal(sshConfigFileName))

	snapshot := &v1.ConfigMap{}
	Expect(client.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: binding.ConfigMap.Name}, snapshot)).Should(Succeed())
	Expect(snapshot.Immutable).ShouldNot(BeNil())
	Expect(*snapshot.Immutable).Should(BeTrue())
	Expect(snapshot.Data).Should(HaveKeyWithValue(sshConfigFileName, sshConfigText))
	Expect(snapshot.Annotations).Should(HaveKeyWithValue(sshConfigSourceAnnotation, userTask.Namespace+"/"+userTask.Name))
}

func sshConfigBinding(tr *pipelinev1.TaskRun) pipelinev1.WorkspaceBinding {
	for _, binding := range tr.Spec.Workspaces {
		if binding.Name == sshConfigWorkspaceName {
			return binding
		}
	}
	Fail("ssh-config workspace was not bound")
	return pipelinev1.WorkspaceBinding{}
}
