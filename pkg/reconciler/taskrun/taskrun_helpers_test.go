// taskrun_helpers_test.go

// This file contains shared helper functions, mock implementations, and setup
// utilities used across the entire taskrun test suite. It is not a test suite
// itself but provides the building blocks for other test files.

package taskrun

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	mpcmetrics "github.com/konflux-ci/multi-platform-controller/pkg/metrics"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"knative.dev/pkg/apis"
)

const (
	systemNamespace = "multi-platform-controller"
	userNamespace   = "default"
)

// setupClientAndReconciler initializes a new fake Kubernetes client and a
// ReconcileTaskRun instance for use in tests. It pre-populates the client
// with the provided runtime objects.
func setupClientAndReconciler(objs []runtimeclient.Object) (runtimeclient.Client, *ReconcileTaskRun) {
	scheme := runtime.NewScheme()
	Expect(pipelinev1.AddToScheme(scheme)).Should(Succeed())
	Expect(v1.AddToScheme(scheme)).Should(Succeed())
	Expect(appsv1.AddToScheme(scheme)).Should(Succeed())

	// We need to tell the fake client about the status subresource for TaskRuns
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithStatusSubresource(&pipelinev1.TaskRun{}).Build()

	// Wrap the fake client to automatically assign UIDs on Create.
	client := &clientWithUIDs{Client: fakeClient}

	reconciler := &ReconcileTaskRun{
		apiReader:         client,
		client:            client,
		scheme:            scheme,
		eventRecorder:     &record.FakeRecorder{},
		operatorNamespace: systemNamespace,
		cloudProviders:    map[string]func(platform string, config map[string]string, systemnamespace string) cloud.CloudProvider{"mock": MockCloudSetup},
		platformConfig:    map[string]PlatformConfig{},
	}
	return client, reconciler
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

// clientWithUIDs is a wrapper around a fake client that adds UIDs on Create,
// to more closely mimic the behavior of a real Kubernetes API server where every
// new object gets a unique identifier
type clientWithUIDs struct {
	runtimeclient.Client
	uidCounter int
}

// Create intercepts the Create call and adds a UID to the object if it doesn't have one.
func (c *clientWithUIDs) Create(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.CreateOption) error {
	metaObj, ok := obj.(metav1.Object)
	if ok {
		if metaObj.GetUID() == "" {
			c.uidCounter++
			metaObj.SetUID(types.UID(fmt.Sprintf("uid-%d", c.uidCounter)))
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

// runSuccessfulProvision simulates a successful provision TaskRun. It updates the
// provision TaskRun's status to Succeeded, creates the corresponding secret that
// the provisioner would have created, and then runs the reconciler to process the result.
func runSuccessfulProvision(ctx context.Context, provision *pipelinev1.TaskRun, client runtimeclient.Client, tr *pipelinev1.TaskRun, reconciler *ReconcileTaskRun) {
	provision.Status.CompletionTime = &metav1.Time{Time: time.Now().Add(time.Hour * -2)}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
	})
	Expect(client.Status().Update(ctx, provision)).ShouldNot(HaveOccurred())

	s := v1.Secret{}
	s.Name = SecretPrefix + tr.Name
	s.Namespace = tr.Namespace
	s.Data = map[string][]byte{}
	s.Data["id_rsa"] = []byte("expected")
	s.Data["host"] = []byte("host")
	s.Data["user-dir"] = []byte("buildir")
	Expect(client.Create(ctx, &s)).ShouldNot(HaveOccurred())

	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	Expect(err).ShouldNot(HaveOccurred())
	secret := getSecret(ctx, client, tr)
	Expect(secret.Data["error"]).Should(BeEmpty())
}

func runSuccessfulProvisionWithConflict(ctx context.Context, provision *pipelinev1.TaskRun, client runtimeclient.Client, tr *pipelinev1.TaskRun, reconciler *ReconcileTaskRun) {
	provision.Status.CompletionTime = &metav1.Time{Time: time.Now().Add(time.Hour * -2)}
	provision.Status.SetCondition(&apis.Condition{
		Type:               apis.ConditionSucceeded,
		Status:             "True",
		LastTransitionTime: apis.VolatileTime{Inner: metav1.Time{Time: time.Now().Add(time.Hour * -2)}},
	})
	Expect(client.Status().Update(ctx, provision)).ShouldNot(HaveOccurred())

	s := v1.Secret{}
	s.Name = SecretPrefix + tr.Name
	s.Namespace = tr.Namespace
	s.Data = map[string][]byte{}
	s.Data["id_rsa"] = []byte("expected")
	s.Data["host"] = []byte("host")
	s.Data["user-dir"] = []byte("buildir")
	Expect(client.Create(ctx, &s)).ShouldNot(HaveOccurred())

	// Update TaskRun externally to simulate Tekton modification during processing
	external := &pipelinev1.TaskRun{}
	Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: tr.Name}, external)).Should(Succeed())
	// if the labels or annotations are nil, we need to make them
	if external.Labels == nil {
		external.Labels = make(map[string]string)
	}
	if external.Annotations == nil {
		external.Annotations = make(map[string]string)
	}
	external.Labels["external-label"] = "external-value"
	external.Annotations["external-annotation"] = "external-value"
	external.Finalizers = append(external.Finalizers, "external-finalizer")
	Expect(client.Update(ctx, external)).Should(Succeed())

	// Create conflicting client that will cause conflict after a succesfull provision
	conflictingClient := &ConflictingClient{
		Client:        client,
		ConflictCount: 1, // Will fail once with conflict, then succeed
	}

	// Create reconciler with conflicting client
	conflictingReconciler := &ReconcileTaskRun{
		client:                   conflictingClient,
		apiReader:                reconciler.apiReader,
		scheme:                   reconciler.scheme,
		eventRecorder:            reconciler.eventRecorder,
		operatorNamespace:        reconciler.operatorNamespace,
		configMapResourceVersion: reconciler.configMapResourceVersion,
		platformConfig:           reconciler.platformConfig,
		cloudProviders:           reconciler.cloudProviders,
	}

	// This reconcile will hit the conflict after a succesfull provision but succeed due to UpdateTaskRunWithRetry
	_, err := conflictingReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: provision.Namespace, Name: provision.Name}})
	Expect(err).ShouldNot(HaveOccurred())

	secret := getSecret(ctx, client, tr)
	Expect(secret.Data["error"]).Should(BeEmpty())
}

// getSecret retrieves the secret associated with a given user TaskRun.

func getSecret(ctx context.Context, client runtimeclient.Client, tr *pipelinev1.TaskRun) *v1.Secret {
	name := SecretPrefix + tr.Name
	secret := v1.Secret{}
	Expect(client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: name}, &secret)).Should(Succeed())
	return &secret
}

// assertNoSecret asserts that the secret for a given user TaskRun does not exist,
// which is used to verify cleanup logic.
func assertNoSecret(ctx context.Context, client runtimeclient.Client, tr *pipelinev1.TaskRun) {
	name := SecretPrefix + tr.Name
	secret := v1.Secret{}
	err := client.Get(ctx, types.NamespacedName{Namespace: tr.Namespace, Name: name}, &secret)
	Expect(errors.IsNotFound(err)).Should(BeTrue())
}

// runUserPipeline encapsulates the common flow of creating a user TaskRun
// and reconciling it until a provisioner TaskRun is created or a host is assigned.
// This helper is used in happy-path scenarios to reduce boilerplate.
func runUserPipeline(ctx context.Context, client runtimeclient.Client, reconciler *ReconcileTaskRun, name string) *pipelinev1.TaskRun {
	createUserTaskRun(ctx, client, name, "linux/arm64")
	// Multiple reconcile calls are needed because the allocation process is asynchronous:
	// 1st call: Detects TaskRun and initiates host allocation
	// 2nd call: Processes allocation result (may launch cloud instance)
	// 3rd call: Assigns host once instance is ready
	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	Expect(err).ShouldNot(HaveOccurred())
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	Expect(err).ShouldNot(HaveOccurred())
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
	Expect(err).ShouldNot(HaveOccurred())
	tr := getUserTaskRun(ctx, client, name)
	if tr.Labels[AssignedHost] == "" {
		Expect(tr.Annotations[CloudInstanceId]).ShouldNot(BeEmpty())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: userNamespace, Name: name}})
		Expect(err).ShouldNot(HaveOccurred())
		tr = getUserTaskRun(ctx, client, name)
	}
	Expect(tr.Labels[AssignedHost]).ShouldNot(BeEmpty())
	return tr
}

// getProvisionTaskRun finds and returns the provisioner TaskRun that was created
// on behalf of a given user TaskRun.
func getProvisionTaskRun(ctx context.Context, client runtimeclient.Client, tr *pipelinev1.TaskRun) *pipelinev1.TaskRun {
	list := pipelinev1.TaskRunList{}
	err := client.List(ctx, &list, &runtimeclient.ListOptions{Namespace: systemNamespace})
	Expect(err).ShouldNot(HaveOccurred())
	for i := range list.Items {
		if list.Items[i].Labels[UserTaskName] == tr.Name && list.Items[i].Labels[UserTaskNamespace] == tr.Namespace {
			return &list.Items[i]
		}
	}
	Expect("could not find task").Should(BeEmpty())
	return nil
}

// ExpectNoProvisionTaskRun asserts that no provisioner TaskRun was created for a
// user TaskRun, which is the expected behavior for certain provisioning types like 'local'.
func ExpectNoProvisionTaskRun(ctx context.Context, client runtimeclient.Client, tr *pipelinev1.TaskRun) {
	list := pipelinev1.TaskRunList{}
	err := client.List(ctx, &list)
	Expect(err).ShouldNot(HaveOccurred())
	foundCount := 0
	for i := range list.Items {
		if list.Items[i].Labels[AssignedHost] == "" && list.Items[i].Labels[UserTaskName] == tr.Name {
			foundCount++
		}
	}
	Expect(foundCount).Should(BeNumerically("==", 0))
}

// getUserTaskRun retrieves a user TaskRun by its name from the default user namespace.
func getUserTaskRun(ctx context.Context, client runtimeclient.Client, name string) *pipelinev1.TaskRun {
	ret := pipelinev1.TaskRun{}
	err := client.Get(ctx, types.NamespacedName{Namespace: userNamespace, Name: name}, &ret)
	Expect(err).ShouldNot(HaveOccurred())
	return &ret
}

// createUserTaskRun creates a basic user TaskRun with the specified name and platform.
func createUserTaskRun(ctx context.Context, client runtimeclient.Client, name string, platform string) {
	tr := &pipelinev1.TaskRun{}
	tr.Namespace = userNamespace
	tr.Name = name
	tr.Labels = map[string]string{"tekton.dev/memberOf": "tasks"}
	tr.Spec = pipelinev1.TaskRunSpec{
		Params: []pipelinev1.Param{{Name: PlatformParam, Value: *pipelinev1.NewStructuredValues(platform)}},
	}
	tr.Status.TaskSpec = &pipelinev1.TaskSpec{Volumes: []v1.Volume{{Name: "test", VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: SecretPrefix + name}}}}}
	Expect(client.Create(ctx, tr)).ShouldNot(HaveOccurred())
}

// createHostConfig is a factory function that returns the necessary Kubernetes
// objects (ConfigMap, Secret) to represent a static host pool configuration.
func createHostConfig() []runtimeclient.Object {
	cm := createHostConfigMap()
	sec := v1.Secret{}
	sec.Name = "awskeys"
	sec.Namespace = systemNamespace
	sec.Labels = map[string]string{MultiPlatformSecretLabel: "true"}
	return []runtimeclient.Object{&cm, &sec}
}

// createHostConfigMap creates the ConfigMap for a static host pool.
func createHostConfigMap() v1.ConfigMap {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"allowed-namespaces":     "default,system-.*",
		"host.host1.address":     "ec2-12-345-67-890.compute-1.amazonaws.com",
		"host.host1.secret":      "awskeys",
		"host.host1.concurrency": "4",
		"host.host1.user":        "ec2-user",
		"host.host1.platform":    "linux/arm64",
		"host.host2.address":     "ec2-09-876-543-210.compute-1.amazonaws.com",
		"host.host2.secret":      "awskeys",
		"host.host2.concurrency": "4",
		"host.host2.user":        "ec2-user",
		"host.host2.platform":    "linux/arm64",
	}
	return cm
}

// createDynamicHostConfig is a factory function for a dynamic provisioning configuration.
func createDynamicHostConfig() []runtimeclient.Object {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"additional-instance-tags":               "foo=bar,key=value",
		"dynamic-platforms":                      "linux/arm64",
		"dynamic.linux-arm64.type":               "mock",
		"dynamic.linux-arm64.region":             "us-east-1",
		"dynamic.linux-arm64.ami":                "ami-03d6a5256a46c9feb",
		"dynamic.linux-arm64.instance-type":      "t4g.medium",
		"dynamic.linux-arm64.key-name":           "sdouglas-arm-test",
		"dynamic.linux-arm64.aws-secret":         "awsiam",
		"dynamic.linux-arm64.ssh-secret":         "awskeys",
		"dynamic.linux-arm64.max-instances":      "2",
		"dynamic.linux-arm64.allocation-timeout": "2",
	}
	sec := v1.Secret{}
	sec.Name = "awskeys"
	sec.Namespace = systemNamespace
	sec.Labels = map[string]string{MultiPlatformSecretLabel: "true"}
	return []runtimeclient.Object{&cm, &sec}
}

// createDynamicPoolHostConfig is a factory function for a dynamic pool configuration.
func createDynamicPoolHostConfig() []runtimeclient.Object {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"additional-instance-tags":          "foo=bar,key=value",
		"dynamic-pool-platforms":            "linux/arm64",
		"dynamic.linux-arm64.type":          "mock",
		"dynamic.linux-arm64.region":        "us-east-1",
		"dynamic.linux-arm64.ami":           "ami-03d6a5256a46c9feb",
		"dynamic.linux-arm64.instance-type": "t4g.medium",
		"dynamic.linux-arm64.key-name":      "sdouglas-arm-test",
		"dynamic.linux-arm64.aws-secret":    "awsiam",
		"dynamic.linux-arm64.ssh-secret":    "awskeys",
		"dynamic.linux-arm64.max-instances": "2",
		"dynamic.linux-arm64.concurrency":   "2",
		"dynamic.linux-arm64.max-age":       "20",
	}
	sec := v1.Secret{}
	sec.Name = "awskeys"
	sec.Namespace = systemNamespace
	sec.Labels = map[string]string{MultiPlatformSecretLabel: "true"}
	return []runtimeclient.Object{&cm, &sec}
}

// createLocalHostConfig is a factory function for a local provisioning configuration.
func createLocalHostConfig() []runtimeclient.Object {
	cm := v1.ConfigMap{}
	cm.Name = HostConfig
	cm.Namespace = systemNamespace
	cm.Labels = map[string]string{ConfigMapLabel: "hosts"}
	cm.Data = map[string]string{
		"local-platforms": "linux/arm64",
	}
	return []runtimeclient.Object{&cm}
}

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
	Running           int
	Terminated        int
	Instances         map[cloud.InstanceIdentifier]MockInstance
	FailGetAddress    bool
	TimeoutGetAddress bool
	FailGetState      bool
	FailCleanUpVMs    bool
}

func (m *MockCloud) ListInstances(kubeClient runtimeclient.Client, ctx context.Context, instanceTag string) ([]cloud.CloudVMInstance, error) {
	ret := []cloud.CloudVMInstance{}
	for _, v := range m.Instances {
		ret = append(ret, v.CloudVMInstance)
	}
	return ret, nil
}

func (m *MockCloud) CountInstances(kubeClient runtimeclient.Client, ctx context.Context, instanceTag string) (int, error) {
	return m.Running, nil
}

func (m *MockCloud) SshUser() string {
	return "root"
}

func (m *MockCloud) LaunchInstance(kubeClient runtimeclient.Client, ctx context.Context, taskRunID string, instanceTag string, additionalTags map[string]string) (cloud.InstanceIdentifier, error) {
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
	m.Running--
	m.Terminated++
	delete(m.Instances, instance)
	return nil
}

func (m *MockCloud) GetInstanceAddress(kubeClient runtimeclient.Client, ctx context.Context, instanceId cloud.InstanceIdentifier) (string, error) {
	if m.FailGetAddress {
		return "", fmt.Errorf("failed")
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
		return "", fmt.Errorf("failed")
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

// In this implementation of the function, the MockInstance's taskRun value is compared to the keys in existingTaskRuns for
// a speedier return.
func (m *MockCloud) CleanUpVms(ctx context.Context, kubeClient runtimeclient.Client, existingTaskRuns map[string][]string) error {
	if m.FailCleanUpVMs {
		return fmt.Errorf("failed")
	}

	var instancesToDelete []string
	for k, v := range m.Instances {
		_, ok := existingTaskRuns[v.taskRun]
		if !ok {
			instancesToDelete = append(instancesToDelete, string(k))
		}
	}

	for _, instance := range instancesToDelete {
		m.Running--
		m.Terminated++
		delete(m.Instances, cloud.InstanceIdentifier(instance))
	}

	return nil
}

func MockCloudSetup(platform string, data map[string]string, systemnamespace string) cloud.CloudProvider {
	return &cloudImpl
}

// ConflictingClient simulates conflict errors for testing
type ConflictingClient struct {
	runtimeclient.Client
	ConflictCount int
	callCount     int
}

func (c *ConflictingClient) Update(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.UpdateOption) error {
	c.callCount++
	if c.callCount <= c.ConflictCount {
		// hardcoded gvk is not perfect, but I dunno how to avoid that :(
		return errors.NewConflict(schema.GroupResource{Group: "tekton.dev", Resource: "taskruns"}, "test", fmt.Errorf("conflict"))
	}
	return c.Client.Update(ctx, obj, opts...)
}

func (c *ConflictingClient) Status() runtimeclient.StatusWriter {
	return &ConflictingStatusWriter{
		StatusWriter:  c.Client.Status(),
		ConflictCount: c.ConflictCount,
		callCount:     &c.callCount,
		client:        c.Client,
	}
}

type ConflictingStatusWriter struct {
	runtimeclient.StatusWriter
	ConflictCount int
	callCount     *int
	client        runtimeclient.Client
}

func (c *ConflictingStatusWriter) Update(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.SubResourceUpdateOption) error {
	*c.callCount++
	if *c.callCount <= c.ConflictCount {
		return errors.NewConflict(schema.GroupResource{Group: "tekton.dev", Resource: "taskruns"}, "test", fmt.Errorf("conflict"))
	}
	return c.client.Status().Update(ctx, obj, opts...)
}

// ErrorClient simulates non-conflict errors for testing
type ErrorClient struct {
	runtimeclient.Client
	Error error
}

func (c *ErrorClient) Update(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.UpdateOption) error {
	return c.Error
}

// Helper function to get counter metric value
func getCounterValue(platform string, counter string) float64 {
	metricDto := &dto.Metric{}
	var pmetrics *mpcmetrics.PlatformMetrics
	mpcmetrics.HandleMetrics(platform, func(metrics *mpcmetrics.PlatformMetrics) {
		pmetrics = metrics
	})
	if pmetrics == nil {
		return -1
	}

	var err error
	switch counter {
	case "cleanup_failures":
		err = pmetrics.CleanupFailures.Write(metricDto)
	case "provisioning_failures":
		err = pmetrics.ProvisionFailures.Write(metricDto)
	case "provisioning_successes":
		err = pmetrics.ProvisionSuccesses.Write(metricDto)
	case "host_allocation_failures":
		err = pmetrics.HostAllocationFailures.Write(metricDto)
	}
	if err != nil {
		return -1
	}
	return metricDto.GetCounter().GetValue()
}
