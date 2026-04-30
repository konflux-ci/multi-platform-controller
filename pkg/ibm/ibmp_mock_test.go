package ibm

import (
	"context"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
)

// mockPowerClient satisfies the powerAPI interface for testing.
// Output/Err fields control return values.
type mockPowerClient struct {
	ListInstancesOutput models.PVMInstances
	ListInstancesErr    error

	LaunchInstanceOutput cloud.InstanceIdentifier
	LaunchInstanceErr    error

	GetInstanceOutput *models.PVMInstance
	GetInstanceErr    error

	DeleteInstanceErr error
}

func (m *mockPowerClient) listInstances(_ context.Context) (models.PVMInstances, error) {
	return m.ListInstancesOutput, m.ListInstancesErr
}

func (m *mockPowerClient) launchInstance(_ context.Context, _ map[string]string) (cloud.InstanceIdentifier, error) {
	return m.LaunchInstanceOutput, m.LaunchInstanceErr
}

func (m *mockPowerClient) getInstance(_ context.Context, _ string) (*models.PVMInstance, error) {
	return m.GetInstanceOutput, m.GetInstanceErr
}

func (m *mockPowerClient) deleteInstance(_ context.Context, _ string) error {
	return m.DeleteInstanceErr
}
