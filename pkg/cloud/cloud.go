package cloud

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const InstanceTag = "multi-platform-instance"
const TaskRunTagKey = "taskRunId"

type CloudProvider interface {
	LaunchInstance(kubeClient client.Client, ctx context.Context, taskRunId string, instanceTag string, additionalInstanceTags map[string]string) (InstanceIdentifier, error)
	TerminateInstance(kubeClient client.Client, ctx context.Context, instance InstanceIdentifier) error
	// GetInstanceAddress this only returns an error if it is a permanent error and the host will not ever be available
	GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId InstanceIdentifier) (string, error)
	CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error)
	ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]CloudVMInstance, error)
	GetState(kubeClient client.Client, ctx context.Context, instanceId InstanceIdentifier) (string, error)
	CleanUpVms(ctx context.Context, kubeClient client.Client, existingTaskRuns map[string][]string) error
	SshUser() string
}

type CloudVMInstance struct {
	InstanceId InstanceIdentifier
	StartTime  time.Time
	Address    string
}

type InstanceIdentifier string

// ValidateTaskRunId ensures that a TaskRunId string is in the following format: '<TaskRun Namespace>:<TaskRun Name>'
func ValidateTaskRunId(taskRunId string) error {
	if strings.Count(taskRunId, ":") == 1 {
		return nil
	}

	return fmt.Errorf("'%s' does not follow the correct format: '<TaskRun Namespace>:<TaskRun Name>'", taskRunId)
}
