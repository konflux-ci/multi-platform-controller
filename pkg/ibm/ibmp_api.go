package ibm

import (
	"context"

	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
)

// powerAPI is the subset of IBM Power Virtual Server operations used by this package.
// The real powerClient satisfies this interface; tests can substitute a mock.
type powerAPI interface {
	listInstances(ctx context.Context) (models.PVMInstances, error)
	launchInstance(ctx context.Context, additionalInfo map[string]string) (cloud.InstanceIdentifier, error)
	getInstance(ctx context.Context, pvmId string) (*models.PVMInstance, error)
	deleteInstance(ctx context.Context, pvmId string) error
}
