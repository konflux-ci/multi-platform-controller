package taskrun

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Retry Logic", func() {
	Describe("isTransientAPIError", func() {
		It("should return false for nil error", func() {
			Expect(isTransientAPIError(nil)).To(BeFalse())
		})

		It("should return true for etcdserver timeout error", func() {
			err := errors.New("etcdserver: request timed out")
			Expect(isTransientAPIError(err)).To(BeTrue())
		})

		It("should return true for error containing etcdserver timeout", func() {
			err := errors.New("failed to list: etcdserver: request timed out, please retry")
			Expect(isTransientAPIError(err)).To(BeTrue())
		})

		It("should return false for non-transient errors", func() {
			err := errors.New("resource not found")
			Expect(isTransientAPIError(err)).To(BeFalse())
		})

		It("should return false for conflict errors", func() {
			err := k8serrors.NewConflict(
				schema.GroupResource{Group: "tekton.dev", Resource: "taskruns"},
				"test-taskrun",
				errors.New("conflict"),
			)
			Expect(isTransientAPIError(err)).To(BeFalse())
		})

		It("should return false for not found errors", func() {
			err := k8serrors.NewNotFound(
				schema.GroupResource{Group: "", Resource: "secrets"},
				"test-secret",
			)
			Expect(isTransientAPIError(err)).To(BeFalse())
		})
	})

	Describe("RetryOnTransientAPIError", func() {
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
		})

		It("should succeed immediately on first try", func() {
			attemptCount := 0
			err := RetryOnTransientAPIError(ctx, func() error {
				attemptCount++
				return nil
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(attemptCount).To(Equal(1))
		})

		It("should retry on transient errors and eventually succeed", func() {
			attemptCount := 0
			err := RetryOnTransientAPIError(ctx, func() error {
				attemptCount++
				if attemptCount < 3 {
					return errors.New("etcdserver: request timed out")
				}
				return nil
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(attemptCount).To(Equal(3))
		})

		It("should stop retrying on permanent errors", func() {
			attemptCount := 0
			permanentErr := errors.New("resource not found")
			err := RetryOnTransientAPIError(ctx, func() error {
				attemptCount++
				return permanentErr
			})

			Expect(err).To(Equal(permanentErr))
			Expect(attemptCount).To(Equal(1))
		})

		It("should exhaust retries and return last error", func() {
			attemptCount := 0
			transientErr := errors.New("etcdserver: request timed out")

			startTime := time.Now()
			err := RetryOnTransientAPIError(ctx, func() error {
				attemptCount++
				return transientErr
			})
			duration := time.Since(startTime)

			Expect(err).To(Equal(transientErr))
			Expect(attemptCount).To(Equal(5))                             // Should try 5 times
			Expect(duration).To(BeNumerically(">", 500*time.Millisecond)) // At least some backoff
		})

		It("should respect context cancellation", func() {
			ctx, cancel := context.WithCancel(context.Background())
			attemptCount := 0

			// Cancel after first attempt
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()

			err := RetryOnTransientAPIError(ctx, func() error {
				attemptCount++
				time.Sleep(200 * time.Millisecond) // Slow operation
				return errors.New("etcdserver: request timed out")
			})

			Expect(err).To(HaveOccurred())
			Expect(attemptCount).To(BeNumerically("<=", 2)) // Should stop early due to cancellation
		})

		It("should use exponential backoff", func() {
			attemptCount := 0
			attemptTimes := []time.Time{}

			err := RetryOnTransientAPIError(ctx, func() error {
				attemptCount++
				attemptTimes = append(attemptTimes, time.Now())
				if attemptCount < 4 {
					return errors.New("etcdserver: request timed out")
				}
				return nil
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(attemptCount).To(Equal(4))

			// Check that delays are increasing
			if len(attemptTimes) >= 3 {
				delay1 := attemptTimes[1].Sub(attemptTimes[0])
				delay2 := attemptTimes[2].Sub(attemptTimes[1])
				// Second delay should be roughly 2x the first (with jitter)
				Expect(delay2).To(BeNumerically(">", delay1))
			}
		})

		It("should respect timeout from context", func() {
			// Create a context with a short timeout
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			attemptCount := 0
			startTime := time.Now()

			err := RetryOnTransientAPIError(ctx, func() error {
				attemptCount++
				// Always return a transient error to force retries
				return errors.New("etcdserver: request timed out")
			})

			duration := time.Since(startTime)

			// Should fail due to context timeout
			Expect(err).To(HaveOccurred())
			// Should stop within a reasonable time after timeout
			Expect(duration).To(BeNumerically("<", 1*time.Second))
			// Should have made at least 1 attempt but stopped early due to timeout
			Expect(attemptCount).To(BeNumerically(">=", 1))
			Expect(attemptCount).To(BeNumerically("<", 5)) // Less than max retries
		})
	})

	Describe("Helper Functions", func() {
		var (
			ctx        context.Context
			mockClient *mockKubeClient
		)

		BeforeEach(func() {
			ctx = context.Background()
			mockClient = &mockKubeClient{}
		})

		Describe("ListWithRetry", func() {
			It("should succeed on first try", func() {
				mockClient.listErr = nil
				mockClient.listCallCount = 0

				list := &corev1.SecretList{}
				err := ListWithRetry(ctx, mockClient, list)

				Expect(err).ToNot(HaveOccurred())
				Expect(mockClient.listCallCount).To(Equal(1))
			})

			It("should retry on transient errors", func() {
				mockClient.listCallCount = 0
				mockClient.listErrFunc = func(count int) error {
					if count < 3 {
						return errors.New("etcdserver: request timed out")
					}
					return nil
				}

				list := &corev1.SecretList{}
				err := ListWithRetry(ctx, mockClient, list)

				Expect(err).ToNot(HaveOccurred())
				Expect(mockClient.listCallCount).To(Equal(3))
			})

			It("should not retry on permanent errors", func() {
				permanentErr := errors.New("invalid request")
				mockClient.listErr = permanentErr
				mockClient.listCallCount = 0

				list := &corev1.SecretList{}
				err := ListWithRetry(ctx, mockClient, list)

				Expect(err).To(Equal(permanentErr))
				Expect(mockClient.listCallCount).To(Equal(1))
			})
		})

		Describe("GetWithRetry", func() {
			It("should succeed on first try", func() {
				mockClient.getErr = nil
				mockClient.getCallCount = 0

				secret := &corev1.Secret{}
				key := client.ObjectKey{Namespace: "test", Name: "test-secret"}
				err := GetWithRetry(ctx, mockClient, key, secret)

				Expect(err).ToNot(HaveOccurred())
				Expect(mockClient.getCallCount).To(Equal(1))
			})

			It("should retry on transient errors", func() {
				mockClient.getCallCount = 0
				mockClient.getErrFunc = func(count int) error {
					if count < 2 {
						return errors.New("etcdserver: request timed out")
					}
					return nil
				}

				secret := &corev1.Secret{}
				key := client.ObjectKey{Namespace: "test", Name: "test-secret"}
				err := GetWithRetry(ctx, mockClient, key, secret)

				Expect(err).ToNot(HaveOccurred())
				Expect(mockClient.getCallCount).To(Equal(2))
			})
		})

		Describe("UpdateWithRetry", func() {
			It("should succeed on first try", func() {
				mockClient.updateErr = nil
				mockClient.updateCallCount = 0

				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test",
					},
				}
				err := UpdateWithRetry(ctx, mockClient, secret)

				Expect(err).ToNot(HaveOccurred())
				Expect(mockClient.updateCallCount).To(Equal(1))
			})

			It("should retry on transient errors", func() {
				mockClient.updateCallCount = 0
				mockClient.updateErrFunc = func(count int) error {
					if count < 2 {
						return errors.New("etcdserver: request timed out")
					}
					return nil
				}

				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test",
					},
				}
				err := UpdateWithRetry(ctx, mockClient, secret)

				Expect(err).ToNot(HaveOccurred())
				Expect(mockClient.updateCallCount).To(Equal(2))
			})
		})
	})
})

// Mock Kubernetes client for testing
type mockKubeClient struct {
	listErr       error
	listErrFunc   func(count int) error
	listCallCount int

	getErr       error
	getErrFunc   func(count int) error
	getCallCount int

	updateErr       error
	updateErrFunc   func(count int) error
	updateCallCount int

	createErr       error
	createErrFunc   func(count int) error
	createCallCount int
}

func (m *mockKubeClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	m.getCallCount++
	if m.getErrFunc != nil {
		return m.getErrFunc(m.getCallCount)
	}
	return m.getErr
}

func (m *mockKubeClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	m.listCallCount++
	if m.listErrFunc != nil {
		return m.listErrFunc(m.listCallCount)
	}
	return m.listErr
}

func (m *mockKubeClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	m.createCallCount++
	if m.createErrFunc != nil {
		return m.createErrFunc(m.createCallCount)
	}
	return m.createErr
}

func (m *mockKubeClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return nil
}

func (m *mockKubeClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	m.updateCallCount++
	if m.updateErrFunc != nil {
		return m.updateErrFunc(m.updateCallCount)
	}
	return m.updateErr
}

func (m *mockKubeClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return nil
}

func (m *mockKubeClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return nil
}

func (m *mockKubeClient) Status() client.StatusWriter {
	return nil
}

func (m *mockKubeClient) Scheme() *runtime.Scheme {
	return nil
}

func (m *mockKubeClient) RESTMapper() meta.RESTMapper {
	return nil
}

func (m *mockKubeClient) SubResource(subResource string) client.SubResourceClient {
	return nil
}

func (m *mockKubeClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}

func (m *mockKubeClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return true, nil
}
