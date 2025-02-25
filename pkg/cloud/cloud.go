package cloud

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const InstanceTag = "multi-platform-instance"

type CloudProvider interface {
	LaunchInstance(kubeClient client.Client, ctx context.Context, name string, instanceTag string, additionalInstanceTags map[string]string) (InstanceIdentifier, error)
	TerminateInstance(kubeClient client.Client, ctx context.Context, instance InstanceIdentifier) error
	// GetInstanceAddress this only returns an error if it is a permanent error and the host will not ever be available
	GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId InstanceIdentifier) (string, error)
	CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error)
	ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]CloudVMInstance, error)
	SshUser() string
}

type CloudVMInstance struct {
	InstanceId InstanceIdentifier
	StartTime  time.Time
	Address    string
}

type InstanceIdentifier string
