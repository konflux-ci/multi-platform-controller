package mpcmetrics

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("TaskRun metrics exporter", func() {
	It("exports running and waiting task gauges", func(ctx SpecContext) {
		// Ensure gauges are registered (ignore AlreadyRegistered)
		_ = metrics.Registry.Register(runningTasksGauge)
		_ = metrics.Registry.Register(waitingTasksGauge)

		sch := runtime.NewScheme()
		Expect(pipelinev1.AddToScheme(sch)).To(Succeed())

		platform := "linux/arm64"
		platformLabelValue := platformLabel(platform) // linux-arm64

		// Create TaskRuns
		trRunningNs1 := &pipelinev1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tr-running-ns1",
				Namespace: "ns1",
				Labels: map[string]string{
					assignedHostLabel:   "host-1",
					targetPlatformLabel: platform,
				},
			},
		}
		trCompleted := &pipelinev1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tr-completed",
				Namespace: "ns1",
				Labels: map[string]string{
					assignedHostLabel:   "host-2",
					targetPlatformLabel: platform,
				},
			},
			Status: pipelinev1.TaskRunStatus{
				TaskRunStatusFields: pipelinev1.TaskRunStatusFields{
					CompletionTime: &metav1.Time{Time: time.Now()},
				},
			},
		}
		trRunningNs2 := &pipelinev1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tr-running-ns2",
				Namespace: "ns2",
				Labels: map[string]string{
					assignedHostLabel:   "host-3",
					targetPlatformLabel: platform,
				},
			},
		}
		trWaitingNs1 := &pipelinev1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tr-waiting-ns1",
				Namespace: "ns1",
				Labels: map[string]string{
					waitingForPlatformLabel: platformLabel(platform),
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(sch).WithObjects(trRunningNs1, trCompleted, trRunningNs2, trWaitingNs1).Build()

		// Export and verify running tasks
		Expect(exportRunningTasks(context.Background(), c)).To(Succeed())
		rNs1, err := getGaugeValue(platformLabelValue, "multi_platform_controller_running_tasks", "ns1")
		Expect(err).ToNot(HaveOccurred())
		Expect(rNs1).To(Equal(1))
		rNs2, err := getGaugeValue(platformLabelValue, "multi_platform_controller_running_tasks", "ns2")
		Expect(err).ToNot(HaveOccurred())
		Expect(rNs2).To(Equal(1))

		// Export and verify waiting tasks
		Expect(exportWaitingTasks(context.Background(), c)).To(Succeed())
		wNs1, err := getGaugeValue(platformLabelValue, "multi_platform_controller_waiting_tasks", "ns1")
		Expect(err).ToNot(HaveOccurred())
		Expect(wNs1).To(Equal(1))
	})
})
