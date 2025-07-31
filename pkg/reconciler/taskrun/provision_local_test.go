// This file contains tests for the 'local' host provisioning strategy.
// This strategy is the simplest: it assigns the TaskRun to run on the same
// node as the controller itself without creating a separate
// provisioner TaskRun. The tests ensure that the correct secret is created
// and that cleanup is handled properly upon TaskRun completion.
package taskrun

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Test Local Host Provisioning", func() {

	var client runtimeclient.Client
	var reconciler *ReconcileTaskRun

	BeforeEach(func() {
		client, reconciler = setupClientAndReconciler(createLocalHostConfig())
	})

	// It verifies the complete happy-path for local provisioning.
	// The test ensures that no provisioner TaskRun is created, a secret with the
	// host "localhost" is correctly generated, and that all resources are
	// cleaned up after the user TaskRun completes.
	It("should allocate a local host correctly", func(ctx SpecContext) {
		tr := runUserPipeline(ctx, GinkgoT(), client, reconciler, "test-local")
		ExpectNoProvisionTaskRun(ctx, GinkgoT(), client, tr)
		secret := getSecret(ctx, client, tr)
		Expect(secret.Data["error"]).To(BeEmpty())
		Expect(secret.Data["host"]).To(Equal([]byte("localhost")))

		// Set user task as complete
		Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, tr)).Should(Succeed())
		tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
		tr.Status.SetCondition(&apis.Condition{
			Type:               apis.ConditionSucceeded,
			Status:             "True",
			LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
		})
		Expect(client.Status().Update(ctx, tr)).Should(Succeed())

		// Run reconciler once more to trigger cleanup
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}})
		Expect(err).ShouldNot(HaveOccurred())

		// Verify that the secret has been cleaned up.
		assertNoSecret(ctx, GinkgoT(), client, tr)
	})
})
