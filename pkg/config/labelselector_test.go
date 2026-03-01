package config_test

import (
	"context"

	"github.com/konflux-ci/multi-platform-controller/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Labelselector", func() {
	var scheme *runtime.Scheme
	var clientBuilder *fake.ClientBuilder

	ns := "multi-platform-controller"

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		clientBuilder = fake.NewClientBuilder().WithScheme(scheme)
	})

	It("returns nil if label selector is not configured", func(ctx context.Context) {
		c := clientBuilder.WithObjects(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.HostConfig,
				Namespace: ns,
			},
			Data: map[string]string{
				"key": "value",
			},
		}).Build()

		Expect(config.ReadConfigurationTaskRunLabelSelector(ctx, c, ns)).To(BeNil())
	})

	It("returns a nil LabelSelector if configuration is empty", func(ctx context.Context) {
		c := clientBuilder.WithObjects(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.HostConfig,
				Namespace: ns,
			},
			Data: map[string]string{
				"taskrun-labelselector": "",
			},
		}).Build()

		ls, err := config.ReadConfigurationTaskRunLabelSelector(ctx, c, ns)
		Expect(err).NotTo(HaveOccurred())
		Expect(ls).To(BeNil())
	})

	DescribeTable("returns a LabelSelector if it is configured and valid", func(ctx context.Context, ls string) {
		c := clientBuilder.WithObjects(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.HostConfig,
				Namespace: ns,
			},
			Data: map[string]string{
				"taskrun-labelselector": ls,
			},
		}).Build()

		Expect(config.ReadConfigurationTaskRunLabelSelector(ctx, c, ns)).NotTo(BeNil())
	},
		Entry("equal", "my-label == my-value"),
		Entry("expression", "my-label in (my-value-1,my-value-2)"),
	)

	It("throws an error if the label selector is invalid", func(ctx context.Context) {
		c := clientBuilder.WithObjects(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.HostConfig,
				Namespace: ns,
			},
			Data: map[string]string{
				"taskrun-labelselector": "aa bb cc",
			},
		}).Build()

		ls, err := config.ReadConfigurationTaskRunLabelSelector(ctx, c, ns)
		Expect(err).To(HaveOccurred())
		Expect(ls).To(BeNil())
	})

	It("throws an error if the ConfigMap doesn't exist", func(ctx context.Context) {
		c := clientBuilder.Build()

		ls, err := config.ReadConfigurationTaskRunLabelSelector(ctx, c, ns)
		Expect(err).To(HaveOccurred())
		Expect(ls).To(BeNil())
	})
})
