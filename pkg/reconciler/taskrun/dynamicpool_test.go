package taskrun

import (
	"errors"

	error_client "github.com/konflux-ci/multi-platform-controller/tests/testing_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

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
})
