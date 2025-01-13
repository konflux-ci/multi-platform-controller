package ibm

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var config = IBMPowerDynamicConfig{
	SystemNamespace: "test-namespace",
	Secret:          "test-secret",
	Key:             "test-key",
	Image:           "test-image",
	Url:             "test-url",
	CRN:             "test-crn",
	Network:         "test-network",
	Cores:           2,
	Memory:          4,
	Disk:            10,
	System:          "test-system",
	UserData:        "test-user-data",
	ProcType:        "test-proc-type",
}

func setupClientAndConfig(config IBMPowerDynamicConfig) (client.Client, *IBMPowerDynamicConfig) {
	scheme := runtime.NewScheme()
	_ = pipelinev1.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	objs := []client.Object{}
	objs = append(objs, &v1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: config.SystemNamespace,
		},
	})
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return client, &config
}

var _ = Describe("IBM PowerPC Tests", func() {

	client, _ := setupClientAndConfig(config)

	DescribeTable("IBM PowerPC Connection Test",
		func(instanceId string, expectedError string) {
			service, err := config.authenticatedService(context.Background(), client)
			Expect(err).NotTo(HaveOccurred())
			_, err = service.GetInstanceAddress(context.Background(), instanceId)
			if expectedError != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedError))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("Positive test", "test-instance-id", ""),
		Entry("Negative test - instance not found", "non-existent-instance-id", "not found"),
		Entry("Negative test - invalid instance id", "invalid-instance-id!", "not found"),
		Entry("Negative test - instance id with special characters", "test-instance-id!@#$%^&amp;*()", "not found"),
		Entry("Negative test - empty instance ID", "", "instance ID cannot be empty"),
		Entry("Negative test - nil service", nil, "service is nil"),
		Entry("Negative test - nil instance ID", nil, "instance ID is nil"),
	)
})
