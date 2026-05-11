package taskrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// MockInstance represents a simulated cloud instance for testing purposes.
type MockInstance struct {
	cloud.CloudVMInstance
	taskRun  string
	statusOK bool
}

// MockCloud is a mock implementation of the cloud.CloudProvider interface,
// allowing tests to simulate cloud provider interactions like launching and
// terminating instances without needing real credentials or infrastructure.
type MockCloud struct {
	Running            int
	Terminated         int
	Instances          map[cloud.InstanceIdentifier]MockInstance
	TerminatedIDs      []cloud.InstanceIdentifier
	FailGetAddress     bool
	TimeoutGetAddress  bool
	FailGetState       bool
	FailLaunch         bool
	FailListInstances  bool
	FailTerminate      bool
	FailCountInstances bool
}

func (m *MockCloud) ListInstances(kubeClient runtimeclient.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	if m.FailListInstances {
		return nil, errors.New("list instances failed")
	}
	ret := make([]cloud.CloudVMInstance, 0, len(m.Instances))
	for _, v := range m.Instances {
		ret = append(ret, v.CloudVMInstance)
	}
	return ret, nil
}

func (m *MockCloud) CountInstances(kubeClient runtimeclient.Client, ctx context.Context, instanceTag string) (int, error) {
	if m.FailCountInstances {
		return 0, errors.New("count instances failed")
	}
	return m.Running, nil
}

func (m *MockCloud) SshUser() string {
	return "root"
}

func (m *MockCloud) LaunchInstance(kubeClient runtimeclient.Client, ctx context.Context, taskRunID string, instanceTag string, additionalTags map[string]string) (cloud.InstanceIdentifier, error) {
	if m.FailLaunch {
		return "", errors.New("launch failed")
	}
	m.Running++
	// Check that taskRunID is the correct format
	if strings.Count(taskRunID, ":") != 1 {
		return "", fmt.Errorf("%s was not of the correct format <namespace>:<name>", taskRunID)
	}
	name := strings.Split(taskRunID, ":")[1]

	addr := string(name) + ".host.com"
	identifier := cloud.InstanceIdentifier(name)
	newInstance := MockInstance{
		CloudVMInstance: cloud.CloudVMInstance{InstanceId: identifier, StartTime: time.Now(), Address: addr},
		taskRun:         string(name) + " task run",
		statusOK:        true,
	}
	m.Instances[identifier] = newInstance
	return identifier, nil
}

func (m *MockCloud) TerminateInstance(kubeClient runtimeclient.Client, ctx context.Context, instance cloud.InstanceIdentifier) error {
	if m.FailTerminate {
		return errors.New("terminate failed")
	}
	m.Running--
	m.Terminated++
	m.TerminatedIDs = append(m.TerminatedIDs, instance)
	delete(m.Instances, instance)
	return nil
}

func (m *MockCloud) GetInstanceAddress(kubeClient runtimeclient.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	if m.FailGetAddress {
		return "", errors.New("failed")
	} else if m.TimeoutGetAddress {
		return "", nil
	}
	addr := m.Instances[instanceId].Address
	if addr == "" {
		addr = string(instanceId) + ".host.com"
		instance := m.Instances[instanceId]
		instance.Address = addr
		m.Instances[instanceId] = instance
	}
	return addr, nil
}

func (m *MockCloud) GetState(kubeClient runtimeclient.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (cloud.VMState, error) {
	if m.FailGetState {
		return "", errors.New("failed")
	}

	instance, ok := m.Instances[instanceId]
	if !ok {
		return "", nil
	}
	if !instance.statusOK {
		return cloud.FailedState, nil
	}
	return cloud.OKState, nil
}

// cloudImpl is a global mock implementation of the cloud.CloudProvider interface.
// It allows tests to simulate cloud instance interactions without making real API calls.
//
// NOTE: This uses a singleton pattern because the MockCloudSetup function (used in
// setupClientAndReconciler) needs to return the same instance that tests can manipulate.
// Each test file that uses cloud functionality MUST reset this state in BeforeEach hooks
// to ensure proper test isolation. See provision_dynamic_test.go and provision_dynamicpool_test.go
// for examples of proper state reset patterns.
var cloudImpl MockCloud = MockCloud{Instances: map[cloud.InstanceIdentifier]MockInstance{}}

func MockCloudSetup(platform string, data map[string]string, systemnamespace string) cloud.CloudProvider {
	return &cloudImpl
}
