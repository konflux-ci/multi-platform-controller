// This file contains tests for the 'local' host provisioning strategy.
// This strategy is the simplest: it assigns the TaskRun to run on the same
// node as the controller itself without creating a separate
// provisioner TaskRun. The tests ensure that the correct secret is created
// and that cleanup is handled properly upon TaskRun completion.
package taskrun

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
		tr := runUserPipeline(ctx, client, reconciler, "test-local")
		ExpectNoProvisionTaskRun(ctx, client, tr)
		secret := getSecret(ctx, client, tr)
		Expect(secret.Data["error"]).Should(BeEmpty())
		Expect(secret.Data["host"]).Should(Equal([]byte("localhost")))

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
		assertNoSecret(ctx, client, tr)
	})

	Describe("Local.Allocate error path", func() {
		It("should return error when client.Update fails", func(ctx SpecContext) {
			s := runtime.NewScheme()
			Expect(pipelinev1.AddToScheme(s)).Should(Succeed())
			Expect(corev1.AddToScheme(s)).Should(Succeed())

			updateErr := errors.New("update failed")
			fakeClient := fake.NewClientBuilder().WithScheme(s).
				WithInterceptorFuncs(interceptor.Funcs{
					Update: func(_ context.Context, _ runtimeclient.WithWatch, _ runtimeclient.Object, _ ...runtimeclient.UpdateOption) error {
						return updateErr
					},
				}).Build()

			r := &ReconcileTaskRun{client: fakeClient, scheme: s}
			tr := &pipelinev1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{Name: "test-tr", Namespace: "default", UID: "uid-1",
					Labels: map[string]string{}},
			}

			_, err := Local{}.Allocate(r, ctx, tr, "test-secret")
			Expect(err).Should(MatchError(updateErr))
		})
	})

	Describe("createUserTaskSecret", func() {
		var (
			s  *runtime.Scheme
			tr *pipelinev1.TaskRun
		)

		BeforeEach(func() {
			s = runtime.NewScheme()
			Expect(pipelinev1.AddToScheme(s)).Should(Succeed())
			Expect(corev1.AddToScheme(s)).Should(Succeed())

			tr = &pipelinev1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{
					Name: "owner-tr", Namespace: "default", UID: "uid-123",
				},
			}
		})

		It("should return nil when the secret already exists", func(ctx SpecContext) {
			existing := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "default"},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
			r := &ReconcileTaskRun{client: fakeClient, scheme: s}

			data := map[string][]byte{"host": []byte("localhost")}
			Expect(createUserTaskSecret(r, ctx, tr, "my-secret", data)).To(Succeed())
		})

		It("should return error when client.Create fails with non-AlreadyExists error", func(ctx SpecContext) {
			createErr := errors.New("create failed")
			fakeClient := fake.NewClientBuilder().WithScheme(s).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(_ context.Context, _ runtimeclient.WithWatch, _ runtimeclient.Object, _ ...runtimeclient.CreateOption) error {
						return createErr
					},
				}).Build()
			r := &ReconcileTaskRun{client: fakeClient, scheme: s}

			err := createUserTaskSecret(r, ctx, tr, "my-secret", map[string][]byte{"host": []byte("localhost")})
			Expect(err).Should(MatchError(createErr))
		})

		It("should return error when SetOwnerReference fails", func(ctx SpecContext) {
			// Use a scheme without TaskRun registered so SetOwnerReference
			// cannot determine the owner's GVK and returns an error.
			emptyScheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(emptyScheme)).Should(Succeed())

			fakeClient := fake.NewClientBuilder().WithScheme(emptyScheme).Build()
			r := &ReconcileTaskRun{client: fakeClient, scheme: emptyScheme}

			err := createUserTaskSecret(r, ctx, tr, "my-secret", map[string][]byte{"host": []byte("localhost")})
			Expect(err).Should(MatchError(ContainSubstring("no kind is registered")))
		})
	})
})
