// Testing UpdateHostPools - that runs the host update task periodically for static host pools.
// The spec checks that:
//	- That one test TaskRun has been created when it should and that none were created when the configuration data is incorrect, that the TaskRun
//	- That the TaskRun created was a host updating TaskRun was created
//	- That the configuration data in the TaskRun spec Params and Workspace contain the test data
//
// There are 13 test cases:
// 	1. A positive test to verify all is working correctly
//	2. A negative test with no configuration data
//	3. A negative test to verify UpdateHostPools only creates TaskRuns for static hosts
//	4. A negative test to verify UpdateHostPools only creates TaskRuns when the spec Param key has the correct syntax
//	5. A negative test to verify data validation on the host address field
//	6. A negative test to verify data validation on the host concurrency field
//	7. Another negative test to data verify on the host concurrency field
//	8. A negative test to verify data validation on the host username field
//	9. A negative test to verify data validation on the host platform field
//	10. A test that a blank or forbidden ssh-config is not mounted on the update task
//	11. A test that a failed owner update deletes the update task and its ssh-config snapshot
//	12. A test that a failed snapshot delete after a failed owner update is logged and leaves the snapshot
//	13. A test that a failed update-task delete keeps the ssh-config snapshot and logs the error

package taskrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const testNamespace = "default"

// hostDataFromTRSpec creates a map[string]string of configuration data that can be compared
// to the test case data, from the TaskRun input
func hostDataFromTRSpec(updateTR v1.TaskRun) map[string]string {
	newHostData := make(map[string]string)

	specParams := updateTR.Spec.Params
	for _, specParam := range specParams {
		switch key := specParam.Name; key {
		case "HOST":
			newHostData["address"] = specParam.Value.StringVal
		case "USER":
			newHostData["user"] = specParam.Value.StringVal
		case "CONCURRENCY":
			newHostData["concurrency"] = specParam.Value.StringVal
		case "PLATFORM":
			newHostData["platform"] = specParam.Value.StringVal
		default:
			// Not really needed
		}
	}

	newHostData["secret"] = updateTR.Spec.Workspaces[0].Secret.SecretName

	return newHostData
}

// testConfigDataFromTestData adds a suffix to the test data to create a key format for the TaskRun Spec Params
// that UpdateHostPools recognizes as having the correct syntax
func testConfigDataFromTestData(testData map[string]string, configKeySuffix string) map[string]string {
	testConfigData := make(map[string]string)

	for k, v := range testData {
		suffixedKey := configKeySuffix + k
		testConfigData[suffixedKey] = v
	}

	return testConfigData
}

// HostUpdateTaskRunTest - Ginkgo table testing spec for HostUpdateTaskRunTest. Creates a new ConfigMap for each
// test case and runs them separately
var _ = Describe("HostUpdateTaskRunTest", func() {
	var scheme *runtime.Scheme
	var hostConfig = &corev1.ConfigMap{}

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		utilruntime.Must(corev1.AddToScheme(scheme))
		utilruntime.Must(v1.AddToScheme(scheme))

		hostConfig = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      HostConfig,
				Namespace: testNamespace,
			},
			Data: map[string]string{"test data": "will replace this"},
		}
	})

	It("should fail when the config doesn't exist", func(ctx SpecContext) {
		// given: the host config configmap doesn't exist
		k8sClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		// when: host pools are updated
		log := logr.FromContextOrDiscard(ctx)
		UpdateHostPools(testNamespace, k8sClient, scheme, record.NewFakeRecorder(10), &log)

		// then: no host pool update tasks are created
		list := v1.TaskRunList{}
		Expect(k8sClient.List(ctx, &list)).To(Succeed())
		Expect(list.Items).To(BeEmpty())
	})

	It("should succeed with a valid host config", func(ctx SpecContext) {
		// We need a waitgroup to synchronize the spawned goroutine in
		// UpdateHostPools with this thread.  Without this, our assertions may run
		// before any taskruns get created, which will cause these tests to flake.
		waitGroup := &sync.WaitGroup{}

		// given: a valid host hostConfigData
		hostConfigData := map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-koko-hazamar-ssh-key",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le",
			"ssh-config":  "Host *\n  ProxyJump bastion\n",
		}
		hostConfig.Data = testConfigDataFromTestData(hostConfigData, "host.koko-hazamar-prod-1.")

		// We expect one creation request, so increment the wait counter by one.
		waitGroup.Add(1)

		k8sClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(hostConfig).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					opts ...client.CreateOption,
				) error {
					err := client.Create(ctx, obj, opts...)
					if _, ok := obj.(*v1.TaskRun); ok {
						waitGroup.Done()
					}
					return err
				},
			}).
			Build()

		// when: host pools are updated
		log := logr.FromContextOrDiscard(ctx)
		UpdateHostPools(testNamespace, k8sClient, scheme, record.NewFakeRecorder(10), &log)

		// when: spawned threads run to completion
		waitGroup.Wait()

		// get list of all TaskRuns, as we cannot predict the name
		createdList := v1.TaskRunList{}

		// then: host pool update task runs are created
		Expect(k8sClient.List(ctx, &createdList, client.InNamespace(testNamespace))).To(Succeed())
		Expect(createdList.Items).To(HaveLen(1))

		// set label field filled correctly
		Expect(createdList.Items[0].Labels).To(HaveKeyWithValue(TaskTypeLabel, TaskTypeUpdate))

		// extract TaskRun data to begin testing individual fields were correctly filled
		updatedHostData := hostDataFromTRSpec(createdList.Items[0])

		delete(hostConfigData, "ssh-config")
		Expect(hostConfigData).To(BeEquivalentTo(updatedHostData))
		Expect(createdList.Items[0].Spec.Workspaces).Should(HaveLen(2))
		Expect(createdList.Items[0].Spec.Workspaces[1].Name).Should(Equal(sshConfigWorkspaceName))
		Expect(createdList.Items[0].Spec.Workspaces[1].ConfigMap.Name).ShouldNot(Equal(HostConfig))
		Expect(createdList.Items[0].Spec.Workspaces[1].ConfigMap.Items[0].Key).Should(Equal(sshConfigFileName))
		snapshot := &corev1.ConfigMap{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: createdList.Items[0].Spec.Workspaces[1].ConfigMap.Name}, snapshot)).Should(Succeed())
			g.Expect(snapshot.Data).Should(HaveKeyWithValue(sshConfigFileName, "Host *\n  ProxyJump bastion"))
			g.Expect(snapshot.Immutable).ShouldNot(BeNil())
			g.Expect(*snapshot.Immutable).Should(BeTrue())
			g.Expect(snapshot.OwnerReferences).Should(ContainElement(HaveField("Name", createdList.Items[0].Name)))
		}).Should(Succeed())
	})

	It("should delete the update task and ssh-config snapshot when setting its owner fails", func(ctx SpecContext) {
		waitGroup := &sync.WaitGroup{}
		hostConfig.Data = testConfigDataFromTestData(map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-koko-hazamar-ssh-key",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le",
			"ssh-config":  "Host *\n  ProxyJump bastion\n",
		}, "host.koko-hazamar-prod-1.")
		waitGroup.Add(1)

		k8sClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(hostConfig).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					opts ...client.CreateOption,
				) error {
					err := client.Create(ctx, obj, opts...)
					if _, ok := obj.(*v1.TaskRun); ok {
						waitGroup.Done()
					}
					return err
				},
				Patch: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					patch client.Patch,
					opts ...client.PatchOption,
				) error {
					return fmt.Errorf("owner patch failed")
				},
			}).
			Build()

		log := logr.FromContextOrDiscard(ctx)
		UpdateHostPools(testNamespace, k8sClient, scheme, record.NewFakeRecorder(10), &log)
		waitGroup.Wait()

		Eventually(func(g Gomega) {
			list := &corev1.ConfigMapList{}
			g.Expect(k8sClient.List(ctx, list, client.InNamespace(testNamespace))).Should(Succeed())
			g.Expect(list.Items).Should(ConsistOf(HaveField("Name", HostConfig)))
			tasks := &v1.TaskRunList{}
			g.Expect(k8sClient.List(ctx, tasks, client.InNamespace(testNamespace))).Should(Succeed())
			g.Expect(tasks.Items).Should(BeEmpty())
		}).Should(Succeed())
	})

	It("should log a snapshot delete failure when setting its owner fails", func(ctx SpecContext) {
		waitGroup := &sync.WaitGroup{}
		hostConfig.Data = testConfigDataFromTestData(map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-koko-hazamar-ssh-key",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le",
			"ssh-config":  "Host *\n  ProxyJump bastion\n",
		}, "host.koko-hazamar-prod-1.")
		waitGroup.Add(1)

		k8sClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(hostConfig).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					opts ...client.CreateOption,
				) error {
					err := client.Create(ctx, obj, opts...)
					if _, ok := obj.(*v1.TaskRun); ok {
						waitGroup.Done()
					}
					return err
				},
				Patch: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					patch client.Patch,
					opts ...client.PatchOption,
				) error {
					return errors.New("owner patch failed")
				},
				Delete: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					opts ...client.DeleteOption,
				) error {
					if _, ok := obj.(*corev1.ConfigMap); ok {
						return errors.New("delete snapshot failed")
					}
					return client.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		var logged []string
		var logMu sync.Mutex
		log := funcr.New(func(_, args string) {
			logMu.Lock()
			defer logMu.Unlock()
			logged = append(logged, args)
		}, funcr.Options{})
		UpdateHostPools(testNamespace, k8sClient, scheme, record.NewFakeRecorder(10), &log)
		waitGroup.Wait()

		Eventually(func(g Gomega) {
			tasks := &v1.TaskRunList{}
			g.Expect(k8sClient.List(ctx, tasks, client.InNamespace(testNamespace))).Should(Succeed())
			g.Expect(tasks.Items).Should(BeEmpty())

			list := &corev1.ConfigMapList{}
			g.Expect(k8sClient.List(ctx, list, client.InNamespace(testNamespace))).Should(Succeed())
			var snapshot *corev1.ConfigMap
			for i := range list.Items {
				if list.Items[i].Name != HostConfig {
					snapshot = &list.Items[i]
				}
			}
			g.Expect(snapshot).ShouldNot(BeNil())
			g.Expect(snapshot.Data).Should(HaveKeyWithValue(sshConfigFileName, "Host *\n  ProxyJump bastion"))

			logMu.Lock()
			messages := append([]string(nil), logged...)
			logMu.Unlock()
			g.Expect(messages).Should(ContainElement(And(
				ContainSubstring("failed to delete ssh-config snapshot"),
				ContainSubstring("delete snapshot failed"),
			)))
		}).Should(Succeed())
	})

	It("should keep the ssh-config snapshot when deleting the update task fails", func(ctx SpecContext) {
		waitGroup := &sync.WaitGroup{}
		hostConfig.Data = testConfigDataFromTestData(map[string]string{
			"address":     "10.130.75.23",
			"secret":      "internal-koko-hazamar-ssh-key",
			"concurrency": "1",
			"user":        "koko_hazamar",
			"platform":    "linux/ppc64le",
			"ssh-config":  "Host *\n  ProxyJump bastion\n",
		}, "host.koko-hazamar-prod-1.")
		waitGroup.Add(1)

		k8sClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(hostConfig).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					opts ...client.CreateOption,
				) error {
					err := client.Create(ctx, obj, opts...)
					if _, ok := obj.(*v1.TaskRun); ok {
						waitGroup.Done()
					}
					return err
				},
				Patch: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					patch client.Patch,
					opts ...client.PatchOption,
				) error {
					return errors.New("owner patch failed")
				},
				Delete: func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					opts ...client.DeleteOption,
				) error {
					if _, ok := obj.(*v1.TaskRun); ok {
						return errors.New("delete update task failed")
					}
					return client.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		var logged []string
		var logMu sync.Mutex
		log := funcr.New(func(_, args string) {
			logMu.Lock()
			defer logMu.Unlock()
			logged = append(logged, args)
		}, funcr.Options{})
		UpdateHostPools(testNamespace, k8sClient, scheme, record.NewFakeRecorder(10), &log)
		waitGroup.Wait()

		Eventually(func(g Gomega) {
			tasks := &v1.TaskRunList{}
			g.Expect(k8sClient.List(ctx, tasks, client.InNamespace(testNamespace))).Should(Succeed())
			g.Expect(tasks.Items).Should(HaveLen(1))

			list := &corev1.ConfigMapList{}
			g.Expect(k8sClient.List(ctx, list, client.InNamespace(testNamespace))).Should(Succeed())
			var snapshot *corev1.ConfigMap
			for i := range list.Items {
				if list.Items[i].Name != HostConfig {
					snapshot = &list.Items[i]
				}
			}
			g.Expect(snapshot).ShouldNot(BeNil())
			g.Expect(snapshot.Data).Should(HaveKeyWithValue(sshConfigFileName, "Host *\n  ProxyJump bastion"))
			g.Expect(tasks.Items[0].Spec.Workspaces[1].ConfigMap.Name).Should(Equal(snapshot.Name))

			logMu.Lock()
			messages := append([]string(nil), logged...)
			logMu.Unlock()
			g.Expect(messages).Should(ContainElement(And(
				ContainSubstring("failed to delete host update task"),
				ContainSubstring("delete update task failed"),
			)))
		}).Should(Succeed())
	})

	DescribeTable("should omit the ssh-config workspace",
		func(ctx SpecContext, sshConfig string) {
			waitGroup := &sync.WaitGroup{}
			hostConfigData := map[string]string{
				"address":     "10.130.75.23",
				"secret":      "internal-koko-hazamar-ssh-key",
				"concurrency": "1",
				"user":        "koko_hazamar",
				"platform":    "linux/ppc64le",
				"ssh-config":  sshConfig,
			}
			hostConfig.Data = testConfigDataFromTestData(hostConfigData, "host.koko-hazamar-prod-1.")
			waitGroup.Add(1)

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(hostConfig).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(
						ctx context.Context,
						client client.WithWatch,
						obj client.Object,
						opts ...client.CreateOption,
					) error {
						err := client.Create(ctx, obj, opts...)
						if _, ok := obj.(*v1.TaskRun); ok {
							waitGroup.Done()
						}
						return err
					},
				}).
				Build()

			log := logr.FromContextOrDiscard(ctx)
			recorder := record.NewFakeRecorder(10)
			UpdateHostPools(testNamespace, k8sClient, scheme, recorder, &log)
			waitGroup.Wait()

			createdList := v1.TaskRunList{}
			Expect(k8sClient.List(ctx, &createdList, client.InNamespace(testNamespace))).Should(Succeed())
			Expect(createdList.Items).Should(HaveLen(1))
			Expect(createdList.Items[0].Spec.Workspaces).Should(HaveLen(1))
			Expect(createdList.Items[0].Spec.Workspaces[0].Name).Should(Equal("ssh"))
			if strings.TrimSpace(sshConfig) == "" {
				Expect(recorder.Events).ShouldNot(Receive())
				return
			}
			Expect(recorder.Events).Should(Receive(ContainSubstring("SSHConfigRejected")))
		},
		Entry("when ssh-config contains a forbidden directive", "Host *\n  ProxyCommand ssh bastion -W %h:%p\n"),
		Entry("when ssh-config is blank", "   "),
	)

	When("Host config is invalid", func() {
		DescribeTable("Updating host pools should not spawn taskruns",
			func(ctx SpecContext, hostConfigData map[string]string, hostSuffix string) {
				// In these tests, we have no way of synchronizing any spawned goroutines
				// with this thread, since we do no expect any to be spawned.  Instead, we
				// will expect the calls to list all taskruns to succeed multiple times with
				// some delays between checks. This is not an ideal check, but it works in
				// practice.  Having these checks helps prevent buggy tests and buggy
				// implementations of UpdateHostPools.

				// given: an invalid host config
				hostConfig.Data = testConfigDataFromTestData(hostConfigData, hostSuffix)

				k8sClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithRuntimeObjects(hostConfig).
					Build()

				// when: host pools are updated
				log := logr.FromContextOrDiscard(ctx)
				UpdateHostPools(testNamespace, k8sClient, scheme, record.NewFakeRecorder(10), &log)

				// test everything in TaskRun creation that is not part of the table testing
				Eventually(func(g Gomega) {
					createdList := v1.TaskRunList{}
					g.Expect(k8sClient.List(ctx, &createdList, client.InNamespace(testNamespace))).To(Succeed())
					g.Expect(createdList.Items).To(BeEmpty())
				}).
					MustPassRepeatedly(3).
					ProbeEvery(time.Second).
					Within(10 * time.Second).
					Should(Succeed())
			},
			Entry("empty data map", map[string]string{}, ""),
			Entry("dynamic host keys", map[string]string{
				"address":     "10.130.75.23",
				"secret":      "internal-moshe-kipod-ssh-key",
				"concurrency": "1",
				"user":        "koko_hazamar",
				"platform":    "linux/ppc64le"},
				"dynamic.moshe-kipod-prod-1."),
			Entry("bad key format", map[string]string{
				"address":     "10.130.75.23",
				"secret":      "internal-prod-ibm-ssh-key",
				"concurrency": "1",
				"user":        "root",
				"platform":    "linux/ppc64le"},
				"host."),
		)
	})
})
