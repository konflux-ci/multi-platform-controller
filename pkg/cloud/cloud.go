package cloud

import (
	"context"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const InstanceTag = "multi-platform-instance"

type CloudProvider interface {
	LaunchInstance(kubeClient client.Client, log *logr.Logger, ctx context.Context, name string, instanceTag string) (InstanceIdentifier, error)
	TerminateInstance(kubeClient client.Client, log *logr.Logger, ctx context.Context, instance InstanceIdentifier) error
	GetInstanceAddress(kubeClient client.Client, log *logr.Logger, ctx context.Context, instanceId InstanceIdentifier) (string, error)
	CountInstances(kubeClient client.Client, log *logr.Logger, ctx context.Context, instanceTag string) (int, error)
	SshUser() string
}

type InstanceIdentifier string
