package cloud

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	InstanceTag           = "multi-platform-instance"
	TaskRunTagKey         = "taskRunID"
	OKState       VMState = "OK"
	FailedState   VMState = "FAILED"
)

// Regular expression for RFC 1123 Label Names, used for K8s namespaces validation for ValidateTaskRunID.
var rfc1123Regex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type CloudProvider interface {
	LaunchInstance(kubeClient client.Client, ctx context.Context, taskRunID string, instanceTag string, additionalInstanceTags map[string]string) (InstanceIdentifier, error)
	TerminateInstance(kubeClient client.Client, ctx context.Context, instance InstanceIdentifier) error
	// GetInstanceAddress this only returns an error if it is a permanent error and the host will not ever be available
	GetInstanceAddress(kubeClient client.Client, ctx context.Context, instanceId InstanceIdentifier) (string, error)
	CountInstances(kubeClient client.Client, ctx context.Context, instanceTag string) (int, error)
	ListInstances(kubeClient client.Client, ctx context.Context, instanceTag string) ([]CloudVMInstance, error)
	GetState(kubeClient client.Client, ctx context.Context, instanceId InstanceIdentifier) (VMState, error)
	CleanUpVms(ctx context.Context, kubeClient client.Client, existingTaskRuns map[string][]string) error
	SshUser() string
}

type CloudVMInstance struct {
	InstanceId InstanceIdentifier
	StartTime  time.Time
	Address    string
}

type InstanceIdentifier string
type VMState string

// validateRFC1123 checks if a string is a valid according to Kubernetes namespace restrictions.
// https://kubernetes.io/docs/concepts/overview/working-with-objects/namespaces/#namespaces-and-dns
// It returns an error message string if invalid, or an empty string if valid.
func validateRFC1123(labelType, label string) error {
	if len(label) > 63 {
		return fmt.Errorf("%s part '%s' is too long for a Kubernetes label (max 63 chars)", labelType, label)
	}
	if !rfc1123Regex.MatchString(label) {
		return fmt.Errorf("%s part '%s' is not a valid RFC 1123 label (must be lowercase alphanumeric characters"+
			" or '-', and must start and end with an alphanumeric character)", labelType, label)
	}
	return nil
}

// ValidateTaskRunID ensures that a TaskRunId string is in the following format: '<TaskRun Namespace>:<TaskRun Name>'
// It also validates that the ID conforms to cloud provider tagging rules and Kubernetes naming conventions.
func ValidateTaskRunID(taskRunID string) error {
	// Check against the most restrictive rule - IBM Cloud/AWS string length rule
	if len(taskRunID) > 128 {
		return fmt.Errorf("taskRunID '%s' cannot be longer than 128 chars", taskRunID)
	}
	// AWS tags cannot start with 'aws:' (case-insensitive)
	if strings.HasPrefix(strings.ToLower(taskRunID), "aws:") {
		return fmt.Errorf("taskRunID '%s' cannot start with 'aws:' (case-insensitive) as per AWS tag restrictions",
			taskRunID)
	}

	// Validate the '<namespace>:<name>' structure
	parts := strings.Split(taskRunID, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("'%s' does not follow the correct format: '<TaskRun Namespace>:<TaskRun Name>', "+
			"neither the namespace nor the name can be empty", taskRunID)
	}

	namespace := parts[0]
	// Validate namespace against Kubernetes RFC 1123 standard
	if err := validateRFC1123("namespace", namespace); err != nil {
		return err
	}

	return nil
}
