// Assisted-by: TAG
package taskrun

import (
	"context"
	"errors"
	"time"

	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	. "github.com/konflux-ci/multi-platform-controller/pkg/constant"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// deallocTestCloud is a lightweight mock cloud provider for Deallocate tests.
// It gives fine-grained control over ListInstances/TerminateInstance behaviour
// without touching the global cloudImpl singleton used by integration tests.
type deallocTestCloud struct {
	instances     []cloud.CloudVMInstance
	failList      bool
	failTerminate bool
	terminatedIDs []cloud.InstanceIdentifier
}

func (m *deallocTestCloud) ListInstances(_ client.Client, _ context.Context, _ string) ([]cloud.CloudVMInstance, error) {
	if m.failList {
		return nil, errors.New("list instances failed")
	}
	return m.instances, nil
}

func (m *deallocTestCloud) TerminateInstance(_ client.Client, _ context.Context, id cloud.InstanceIdentifier) error {
	if m.failTerminate {
		return errors.New("terminate failed")
	}
	m.terminatedIDs = append(m.terminatedIDs, id)
	return nil
}

func (m *deallocTestCloud) LaunchInstance(_ client.Client, _ context.Context, _ string, _ string, _ map[string]string) (cloud.InstanceIdentifier, error) {
	return "", nil
}
func (m *deallocTestCloud) GetInstanceAddress(_ client.Client, _ context.Context, _ cloud.InstanceIdentifier) (string, error) {
	return "", nil
}
func (m *deallocTestCloud) CountInstances(_ client.Client, _ context.Context, _ string) (int, error) {
	return len(m.instances), nil
}
func (m *deallocTestCloud) GetState(_ client.Client, _ context.Context, _ cloud.InstanceIdentifier) (cloud.VMState, error) {
	return cloud.OKState, nil
}
func (m *deallocTestCloud) SshUser() string { return "test-user" }

var _ = Describe("DynamicHostPool test", func() {
	var (
		dhp DynamicHostPool
		r   *ReconcileTaskRun
		s   *runtime.Scheme
	)

	BeforeEach(func() {
		// Initialize the scheme and add the TaskRun type
		s = runtime.NewScheme()
		Expect(v1.AddToScheme(s)).To(Succeed())
		r = &ReconcileTaskRun{}
	})

	// basically tests the three scenarios handles by isHostIdle -
	//	1. Host does not have TaskRuns
	//	2. Host has a TaskRun with the sought-after selectedHost in its Labels
	// 	3. Extracting the Labels data from the TaskRun while looking for the selectedHost caused an error.
	Describe("isHostIdle", func() {
		It("should return true if the host is idle", func(ctx SpecContext) {
			// Create a mock client with no TaskRun resources
			client := fake.NewClientBuilder().WithScheme(s).Build()
			r.client = client

			idle, err := dhp.isHostIdle(r, ctx, "idle-host")
			Expect(err).NotTo(HaveOccurred())
			Expect(idle).To(BeTrue())
		})

		It("should return false if the host is not idle", func(ctx SpecContext) {
			// Create a mock client with a TaskRun resource assigned to the host
			selectedHost := "not-idle-host"

			tr := &v1.TaskRun{}
			tr.Name = "test-taskrun"
			tr.Namespace = "default"
			tr.Labels = map[string]string{AssignedHost: selectedHost}

			client := fake.NewClientBuilder().WithScheme(s).WithObjects(tr).Build()
			r.client = client

			idle, err := dhp.isHostIdle(r, ctx, selectedHost)
			Expect(err).NotTo(HaveOccurred())
			Expect(idle).To(BeFalse())
		})

		It("should return false if an error occurs", func(ctx SpecContext) {
			// Create a fake client which will return an error, for testing the Error Occurred scenario
			errToReturn := errors.New("fake error")
			//client := error_client.NewErrorClient(s, errToReturn)
			client := fake.NewClientBuilder().WithScheme(s).
				WithInterceptorFuncs(interceptor.Funcs{
					List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
						return errToReturn
					},
				}).Build()
			r.client = client

			// Call the function with a selected host
			selectedHost := "test-host"
			idle, err := dhp.isHostIdle(r, ctx, selectedHost)

			// Verify that the error is returned and the host is not considered idle
			Expect(err).To(MatchError(errToReturn))
			Expect(idle).To(BeFalse())
		})
	})

	Describe("Deallocate", func() {
		var (
			mockCloud *deallocTestCloud
			tr        *v1.TaskRun
		)

		BeforeEach(func() {
			mockCloud = &deallocTestCloud{}
			dhp = DynamicHostPool{
				cloudProvider: mockCloud,
				platform:      "linux/arm64",
				maxAge:        10 * time.Minute,
				concurrency:   2,
				sshSecret:     "test-ssh-secret",
				instanceTag:   "test-tag",
			}
			r.operatorNamespace = "multi-platform-controller"

			tr = &v1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-task",
					Namespace: "default",
				},
			}
		})

		It("should return error when buildHostPool fails", func(ctx SpecContext) {
			mockCloud.failList = true
			r.client = fake.NewClientBuilder().WithScheme(s).Build()

			err := dhp.Deallocate(r, ctx, tr, "secret-name", "any-host")

			Expect(err).To(MatchError(ContainSubstring("list instances failed")))
		})

		It("should return error when inner hostPool.Deallocate fails", func(ctx SpecContext) {
			// A young instance so it lands in the pool and HostPool.Deallocate
			// tries to create a cleanup TaskRun.
			selectedHost := "young-host"
			mockCloud.instances = []cloud.CloudVMInstance{
				{InstanceId: cloud.InstanceIdentifier(selectedHost), Address: "1.2.3.4", StartTime: time.Now()},
			}

			// Intercept Create to make the cleanup TaskRun creation fail
			createErr := errors.New("create failed")
			fakeClient := fake.NewClientBuilder().WithScheme(s).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
						return createErr
					},
				}).Build()
			r.client = fakeClient

			err := dhp.Deallocate(r, ctx, tr, "secret-name", selectedHost)

			Expect(err).To(MatchError(createErr))
		})

		It("should succeed with no old instances and not terminate anything", func(ctx SpecContext) {
			selectedHost := "young-host"
			mockCloud.instances = []cloud.CloudVMInstance{
				{InstanceId: cloud.InstanceIdentifier(selectedHost), Address: "1.2.3.4", StartTime: time.Now()},
			}
			r.client = fake.NewClientBuilder().WithScheme(s).Build()

			err := dhp.Deallocate(r, ctx, tr, "secret-name", selectedHost)

			Expect(err).NotTo(HaveOccurred())
			Expect(mockCloud.terminatedIDs).To(BeEmpty())
		})

		It("should not terminate selectedHost when it is a current host even if old instances exist", func(ctx SpecContext) {
			selectedHost := "young-host"
			mockCloud.instances = []cloud.CloudVMInstance{
				{InstanceId: cloud.InstanceIdentifier(selectedHost), Address: "1.2.3.4", StartTime: time.Now()},
				{InstanceId: "old-host", Address: "5.6.7.8", StartTime: time.Now().Add(-1 * time.Hour)},
			}
			// No TaskRuns assigned to old-host, so buildHostPool will terminate it during its sweep
			r.client = fake.NewClientBuilder().WithScheme(s).Build()

			err := dhp.Deallocate(r, ctx, tr, "secret-name", selectedHost)

			Expect(err).NotTo(HaveOccurred())
			// old-host may have been terminated by buildHostPool's sweep (it's idle),
			// but selectedHost must NOT be terminated
			for _, id := range mockCloud.terminatedIDs {
				Expect(string(id)).NotTo(Equal(selectedHost))
			}
		})

		It("should terminate an old idle selectedHost", func(ctx SpecContext) {
			selectedHost := "old-idle-host"
			mockCloud.instances = []cloud.CloudVMInstance{
				{InstanceId: cloud.InstanceIdentifier(selectedHost), Address: "1.2.3.4", StartTime: time.Now().Add(-1 * time.Hour)},
			}
			// No TaskRuns assigned → host is idle
			r.client = fake.NewClientBuilder().WithScheme(s).Build()

			err := dhp.Deallocate(r, ctx, tr, "secret-name", selectedHost)

			Expect(err).NotTo(HaveOccurred())
			Expect(mockCloud.terminatedIDs).To(ContainElement(cloud.InstanceIdentifier(selectedHost)))
		})

		It("should not terminate an old selectedHost when other tasks are still using it", func(ctx SpecContext) {
			selectedHost := "old-busy-host"
			mockCloud.instances = []cloud.CloudVMInstance{
				{InstanceId: cloud.InstanceIdentifier(selectedHost), Address: "1.2.3.4", StartTime: time.Now().Add(-1 * time.Hour)},
			}

			// A TaskRun is still assigned to this host, making it non-idle
			busyTr := &v1.TaskRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-task",
					Namespace: "default",
					Labels:    map[string]string{AssignedHost: selectedHost},
				},
			}
			r.client = fake.NewClientBuilder().WithScheme(s).WithObjects(busyTr).Build()

			err := dhp.Deallocate(r, ctx, tr, "secret-name", selectedHost)

			Expect(err).NotTo(HaveOccurred())
			// buildHostPool also checks idleness for old hosts; with a TaskRun
			// assigned, neither buildHostPool nor Deallocate should terminate it
			for _, id := range mockCloud.terminatedIDs {
				Expect(string(id)).NotTo(Equal(selectedHost))
			}
		})

		It("should return error when isHostIdle fails for old selectedHost", func(ctx SpecContext) {
			selectedHost := "old-host"
			mockCloud.instances = []cloud.CloudVMInstance{
				{InstanceId: cloud.InstanceIdentifier(selectedHost), Address: "1.2.3.4", StartTime: time.Now().Add(-1 * time.Hour)},
			}

			listErr := errors.New("list error")
			r.client = fake.NewClientBuilder().WithScheme(s).
				WithInterceptorFuncs(interceptor.Funcs{
					List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
						return listErr
					},
				}).Build()

			err := dhp.Deallocate(r, ctx, tr, "secret-name", selectedHost)

			Expect(err).To(MatchError(listErr))
		})

		It("should return error when TerminateInstance fails for old idle selectedHost", func(ctx SpecContext) {
			selectedHost := "old-idle-host"
			mockCloud.instances = []cloud.CloudVMInstance{
				{InstanceId: cloud.InstanceIdentifier(selectedHost), Address: "1.2.3.4", StartTime: time.Now().Add(-1 * time.Hour)},
			}
			mockCloud.failTerminate = true
			// No TaskRuns assigned → host is idle
			r.client = fake.NewClientBuilder().WithScheme(s).Build()

			err := dhp.Deallocate(r, ctx, tr, "secret-name", selectedHost)

			Expect(err).To(MatchError(ContainSubstring("terminate failed")))
		})
	})
})
