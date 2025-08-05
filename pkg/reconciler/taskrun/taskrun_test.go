// This file contains tests for the general functionality of the TaskRun reconciler.
// It covers logic that is not specific to any single provisioning method, such as
// configuration parsing for all types, platform extraction from TaskRun parameters,
// basic failure modes, and the API client's retry mechanisms.
package taskrun

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("TaskRun Reconciler General Tests", func() {

	// This section verifies that the reconciler can correctly parse the main
	// ConfigMap for all supported provisioning types.
	Describe("Test Config Map Parsing", func() {
		It("should parse the static host ConfigMap correctly", func(ctx SpecContext) {
			_, reconciler := setupClientAndReconciler(createHostConfig())
			configIface, err := reconciler.readConfiguration(ctx, "linux/arm64", userNamespace)
			config := configIface.(HostPool)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(len(config.hosts)).Should(Equal(2))
			Expect(config.hosts["host1"].Platform).Should(Equal("linux/arm64"))
		})

		It("should parse the local host ConfigMap correctly", func(ctx SpecContext) {
			_, reconciler := setupClientAndReconciler(createLocalHostConfig())
			configIface, err := reconciler.readConfiguration(ctx, "linux/arm64", userNamespace)
			Expect(configIface).Should(BeAssignableToTypeOf(Local{}))
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("should parse the dynamic host ConfigMap correctly", func(ctx SpecContext) {
			_, reconciler := setupClientAndReconciler(createDynamicHostConfig())
			configIface, err := reconciler.readConfiguration(ctx, "linux/arm64", userNamespace)
			config := configIface.(DynamicResolver)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("foo", "bar"))
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("key", "value"))
		})

		It("should parse the dynamic pool ConfigMap correctly", func(ctx SpecContext) {
			_, reconciler := setupClientAndReconciler(createDynamicPoolHostConfig())
			configIface, err := reconciler.readConfiguration(ctx, "linux/arm64", userNamespace)
			config := configIface.(DynamicHostPool)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("foo", "bar"))
			Expect(config.additionalInstanceTags).Should(HaveKeyWithValue("key", "value"))
		})
	})

	// This section tests the utility function responsible for extracting the
	// target platform from a TaskRun's parameters.
	Describe("Test extractPlatform function", func() {
		It("should extract platform from TaskRun parameters successfully", func() {
			tr := &pipelinev1.TaskRun{
				Spec: pipelinev1.TaskRunSpec{
					Params: []pipelinev1.Param{
						{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues("linux/amd64")},
					},
				},
			}

			platform, err := extractPlatform(tr)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(platform).Should(Equal("linux/amd64"))
		})

		It("should return error when PlatformParam parameter is missing", func() {
			tr := &pipelinev1.TaskRun{
				Spec: pipelinev1.TaskRunSpec{
					Params: []pipelinev1.Param{
						{Name: "OTHER_PARAM", Value: *pipelinev1.NewStructuredValues("other_value")},
					},
				},
			}

			_, err := extractPlatform(tr)
			Expect(err).Should(MatchError(errFailedToDeterminePlatform))
		})
	})

	// This section tests the controller's behavior in edge cases where no suitable
	// hosts can be found for a TaskRun.
	Describe("Test reconciler behavior when no hosts are configured", func() {
		var client runtimeclient.Client
		var reconciler *ReconcileTaskRun

		// It verifies that if no host ConfigMap exists at all, the TaskRun
		// is immediately failed with an error secret.
		It("should create an error secret if no host config exists", func(ctx SpecContext) {
			client, reconciler = setupClientAndReconciler([]runtimeclient.Object{})
			createUserTaskRun(ctx, client, "test-no-config", "linux/arm64")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-no-config"}})
			Expect(err).ShouldNot(HaveOccurred())
			tr := getUserTaskRun(ctx, client, "test-no-config")

			secret := getSecret(ctx, client, tr)
			Expect(secret.Data["error"]).ShouldNot(BeEmpty())
		})

		// It verifies that if a host config exists but contains no hosts for the
		// requested platform, the TaskRun is failed with an error secret.
		It("should create an error secret if no host with the requested platform exists", func(ctx SpecContext) {
			client, reconciler = setupClientAndReconciler(createHostConfig())
			createUserTaskRun(ctx, client, "test-no-platform", "powerpc")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: "test-no-platform"}})
			Expect(err).Should(HaveOccurred())
			tr := getUserTaskRun(ctx, client, "test-no-platform")

			secret := getSecret(ctx, client, tr)
			Expect(secret.Data["error"]).ShouldNot(BeEmpty())
		})
	})

	// This section tests the UpdateTaskRunWithRetry function, which provides
	// resiliency against optimistic locking conflicts when updating TaskRun objects.
	Describe("Test UpdateTaskRunWithRetry function", func() {
		var client runtimeclient.Client
		var tr *pipelinev1.TaskRun

		BeforeEach(func() {
			client, _ = setupClientAndReconciler(createHostConfig())
			tr = &pipelinev1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-taskrun",
					Namespace: userNamespace,
					Labels: map[string]string{
						"existing-label": "existing-value",
					},
					Annotations: map[string]string{
						"existing-annotation": "existing-value",
					},
					Finalizers: []string{"existing-finalizer"},
				},
				Spec: pipelinev1.TaskRunSpec{
					Params: []pipelinev1.Param{
						{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues("linux/amd64")},
					},
				},
			}
			Expect(client.Create(context.Background(), tr)).Should(Succeed())
		})

		It("should update TaskRun successfully on first attempt", func(ctx SpecContext) {
			// Modify the TaskRun
			tr.Labels["new-label"] = "new-value"
			tr.Annotations["new-annotation"] = "new-value"
			tr.Finalizers = append(tr.Finalizers, "new-finalizer")

			// Update should succeed immediately
			err := UpdateTaskRunWithRetry(ctx, client, client, tr)
			Expect(err).ShouldNot(HaveOccurred())

			// Verify the update was applied
			updated := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, updated)).Should(Succeed())
			Expect(updated.Labels).Should(HaveKeyWithValue("new-label", "new-value"))
			Expect(updated.Labels).Should(HaveKeyWithValue("existing-label", "existing-value"))
			Expect(updated.Annotations).Should(HaveKeyWithValue("new-annotation", "new-value"))
			Expect(updated.Annotations).Should(HaveKeyWithValue("existing-annotation", "existing-value"))
			Expect(updated.Finalizers).Should(ContainElements("existing-finalizer", "new-finalizer"))
		})

		// It verifies that the retry logic can successfully handle and merge
		// changes after a simulated conflict error, ensuring that concurrent
		// updates do not cause data loss.
		It("should handle conflict errors with retry and merge", func(ctx SpecContext) {
			// Create a conflicting client that will cause conflicts
			conflictingClient := &ConflictingClient{
				Client:        client,
				ConflictCount: 2, // Will fail twice, then succeed
			}

			// Modify the TaskRun
			tr.Labels[TargetPlatformLabel] = "conflict-value"
			tr.Annotations[AllocationStartTimeAnnotation] = "conflict-value"
			tr.Annotations[CloudInstanceId] = "conflict-value"
			tr.Finalizers = append(tr.Finalizers, PipelineFinalizer)

			// Should succeed after retries
			err := UpdateTaskRunWithRetry(ctx, conflictingClient, client, tr)
			Expect(err).ShouldNot(HaveOccurred())

			// Verify the update was applied
			updated := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, updated)).Should(Succeed())
			Expect(updated.Labels).Should(HaveKeyWithValue(TargetPlatformLabel, "conflict-value"))
			Expect(updated.Annotations).Should(And(
				HaveKeyWithValue(AllocationStartTimeAnnotation, "conflict-value"),
				HaveKeyWithValue(CloudInstanceId, "conflict-value"),
			))
			Expect(updated.Finalizers).Should(ContainElement(PipelineFinalizer))
		})

		// It simulates a real-world race condition where an external actor modifies
		// the TaskRun while our controller is processing it. The test verifies
		// that our changes are correctly merged with the external ones upon retry.
		It("should merge labels and annotations correctly after conflict", func(ctx SpecContext) {
			// First, update the TaskRun externally to simulate concurrent modification
			external := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, external)).Should(Succeed())
			external.Labels["external-label"] = "external-value"
			external.Annotations["external-annotation"] = "external-value"
			external.Finalizers = append(external.Finalizers, "external-finalizer")
			Expect(client.Update(ctx, external)).Should(Succeed())

			// Now modify our local copy with different changes
			tr.Labels[TargetPlatformLabel] = "local-value"
			tr.Annotations[AllocationStartTimeAnnotation] = "local-value"
			tr.Finalizers = append(tr.Finalizers, PipelineFinalizer)

			// Create a client that will cause one conflict
			conflictingClient := &ConflictingClient{
				Client:        client,
				ConflictCount: 1,
			}

			// Update should succeed and merge both changes
			err := UpdateTaskRunWithRetry(ctx, conflictingClient, conflictingClient, tr)
			Expect(err).ShouldNot(HaveOccurred())

			// Verify both sets of changes are present
			updated := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, updated)).Should(Succeed())
			Expect(updated.Labels).Should(HaveKeyWithValue(TargetPlatformLabel, "local-value"))
			Expect(updated.Labels).Should(HaveKeyWithValue("external-label", "external-value"))
			Expect(updated.Labels).Should(HaveKeyWithValue("existing-label", "existing-value"))
			Expect(updated.Annotations).Should(HaveKeyWithValue(AllocationStartTimeAnnotation, "local-value"))
			Expect(updated.Annotations).Should(HaveKeyWithValue("external-annotation", "external-value"))
			Expect(updated.Annotations).Should(HaveKeyWithValue("existing-annotation", "existing-value"))
			Expect(updated.Finalizers).Should(ConsistOf("existing-finalizer", PipelineFinalizer, "external-finalizer"))
		})

		// It tests that the update function does not panic or error when the
		// TaskRun's label or annotation maps are initially nil.
		It("should handle nil maps gracefully", func(ctx SpecContext) {
			// Create TaskRun with nil maps
			nilTr := &pipelinev1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nil-taskrun",
					Namespace: userNamespace,
				},
				Spec: pipelinev1.TaskRunSpec{
					Params: []pipelinev1.Param{
						{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues("linux/amd64")},
					},
				},
			}
			Expect(client.Create(ctx, nilTr)).Should(Succeed())

			// Add some data to nil maps
			nilTr.Labels = map[string]string{TargetPlatformLabel: "new-value"}
			nilTr.Annotations = map[string]string{AllocationStartTimeAnnotation: "new-value"}
			nilTr.Finalizers = []string{PipelineFinalizer}

			err := UpdateTaskRunWithRetry(ctx, client, client, nilTr)
			Expect(err).ShouldNot(HaveOccurred())

			// Verify the update was applied
			updated := &pipelinev1.TaskRun{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: nilTr.Namespace, Name: nilTr.Name}, updated)).Should(Succeed())
			Expect(updated.Labels).Should(HaveKeyWithValue(TargetPlatformLabel, "new-value"))
			Expect(updated.Annotations).Should(HaveKeyWithValue(AllocationStartTimeAnnotation, "new-value"))
			Expect(updated.Finalizers).Should(ContainElement(PipelineFinalizer))
		})

		// It ensures that the retry loop exits immediately for errors that are
		// not optimistic locking conflicts.
		It("should fail immediately on non-conflict errors", func(ctx SpecContext) {
			// Create a client that returns non-conflict errors
			errorClient := &ErrorClient{
				Client: client,
				Error:  fmt.Errorf("some other error"),
			}

			tr.Labels["error-label"] = "error-value"

			err := UpdateTaskRunWithRetry(ctx, errorClient, errorClient, tr)
			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("some other error"))
		})

		// It verifies that the function gives up after a maximum number of retries
		// to prevent an infinite loop in the case of persistent conflicts.
		It("should fail after max retries on persistent conflicts", func(ctx SpecContext) {
			// Create a client that always returns conflicts
			conflictingClient := &ConflictingClient{
				Client:        client,
				ConflictCount: 10, // More than max retries
			}

			tr.Labels["persistent-conflict"] = "value"

			err := UpdateTaskRunWithRetry(ctx, conflictingClient, conflictingClient, tr)
			Expect(err).Should(HaveOccurred())
		})
	})

})
