package mpcmetrics

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"sync"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const (
	assignedHostLabel       = "build.appstudio.redhat.com/assigned-host"
	targetPlatformLabel     = "build.appstudio.redhat.com/target-platform"
	waitingForPlatformLabel = "build.appstudio.redhat.com/waiting-for-platform"
)

var (
	runningTasksGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: MetricsSubsystem,
		Name:      "running_tasks",
		Help:      "The number of currently running tasks on a platform",
	}, []string{"platform", "taskrun_namespace"})

	waitingTasksGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: MetricsSubsystem,
		Name:      "waiting_tasks",
		Help:      "The number of tasks waiting for an executor to be available to run",
	}, []string{"platform", "taskrun_namespace"})

	// Protect gauge reset+increment sequences from concurrent exporter runs
	runningGaugeMu sync.Mutex
	waitingGaugeMu sync.Mutex
)

// AddTaskRunMetricsExporter starts a periodic exporter that updates RunningTasks and WaitingTasks gauges every minute.
// It relies on per-platform metrics having been registered by the reconciler; if not registered yet, updates are skipped.
func AddTaskRunMetricsExporter(mgr ctrl.Manager) error {
	return mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		log := ctrl.Log.WithName("metrics-exporter")
		if ok := mgr.GetCache().WaitForCacheSync(ctx); !ok {
			return context.Canceled
		}
		// Register exporter gauges (idempotent register attempts will error; ignore AlreadyRegistered)
		_ = metrics.Registry.Register(runningTasksGauge)
		_ = metrics.Registry.Register(waitingTasksGauge)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		log.Info("starting taskrun metrics exporter")
		for {
			select {
			case <-ctx.Done():
				log.Info("stopping taskrun metrics exporter")
				return nil
			case <-ticker.C:
				if err := exportRunningTasks(ctx, mgr.GetClient()); err != nil {
					log.Error(err, "failed exporting running tasks")
				}
				if err := exportWaitingTasks(ctx, mgr.GetClient()); err != nil {
					log.Error(err, "failed exporting waiting tasks")
				}
			}
		}
	}))
}

func exportRunningTasks(ctx context.Context, c client.Client) error {
	log := logr.FromContextOrDiscard(ctx)
	var trList pipelinev1.TaskRunList
	req, err := labels.NewRequirement(assignedHostLabel, selection.Exists, []string{})
	if err != nil {
		return err
	}
	ls := labels.NewSelector().Add(*req)
	if err := c.List(ctx, &trList, &client.ListOptions{LabelSelector: ls}); err != nil {
		return err
	}
	runningGaugeMu.Lock()
	defer runningGaugeMu.Unlock()
	runningTasksGauge.Reset()
	for _, tr := range trList.Items {
		if tr.Status.CompletionTime != nil {
			continue
		}
		platform := tr.Labels[targetPlatformLabel]
		if platform == "" {
			continue
		}
		p := platformLabel(platform)
		runningTasksGauge.WithLabelValues(p, tr.Namespace).Inc()
	}
	log.V(1).Info("exported running tasks", "items", len(trList.Items))
	return nil
}

func exportWaitingTasks(ctx context.Context, c client.Client) error {
	log := logr.FromContextOrDiscard(ctx)
	var trList pipelinev1.TaskRunList
	req, err := labels.NewRequirement(waitingForPlatformLabel, selection.Exists, []string{})
	if err != nil {
		return err
	}
	ls := labels.NewSelector().Add(*req)
	if err := c.List(ctx, &trList, &client.ListOptions{LabelSelector: ls}); err != nil {
		return err
	}
	waitingGaugeMu.Lock()
	defer waitingGaugeMu.Unlock()
	waitingTasksGauge.Reset()
	for _, tr := range trList.Items {
		platform := tr.Labels[waitingForPlatformLabel]
		if platform == "" {
			continue
		}
		p := platformLabel(platform)
		waitingTasksGauge.WithLabelValues(p, tr.Namespace).Inc()
	}
	log.V(1).Info("exported waiting tasks", "items", len(trList.Items))
	return nil
}
