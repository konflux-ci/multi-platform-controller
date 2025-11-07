package mpcmetrics

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/constant"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
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
)

type metricLabels struct {
	platform  string
	namespace string
}

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
		ticker := time.NewTicker(15 * time.Second)
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
	req, err := labels.NewRequirement(constant.AssignedHost, selection.Exists, []string{})
	if err != nil {
		return err
	}
	ls := labels.NewSelector().Add(*req)
	if err := c.List(ctx, &trList, &client.ListOptions{LabelSelector: ls}); err != nil {
		return err
	}
	runningTasksGauge.Reset()
	rts := map[metricLabels]float64{}
	for _, tr := range trList.Items {
		if tr.Status.CompletionTime != nil {
			continue
		}
		platform := tr.Labels[constant.TargetPlatformLabel]
		if platform == "" {
			continue
		}
		k := metricLabels{platform: platformLabel(platform), namespace: tr.Namespace}
		if _, ok := rts[k]; !ok {
			rts[k] = .0
		}
		rts[k]++
	}
	for k, v := range rts {
		runningTasksGauge.WithLabelValues(k.platform, k.namespace).Set(v)
	}
	log.V(1).Info("exported running tasks", "items", len(trList.Items))
	return nil
}

func exportWaitingTasks(ctx context.Context, c client.Client) error {
	log := logr.FromContextOrDiscard(ctx)
	var trList pipelinev1.TaskRunList
	req, err := labels.NewRequirement(constant.WaitingForPlatformLabel, selection.Exists, []string{})
	if err != nil {
		return err
	}
	ls := labels.NewSelector().Add(*req)
	if err := c.List(ctx, &trList, &client.ListOptions{LabelSelector: ls}); err != nil {
		return err
	}
	waitingTasksGauge.Reset()
	wts := map[metricLabels]float64{}
	for _, tr := range trList.Items {
		platform := tr.Labels[constant.WaitingForPlatformLabel]
		if platform == "" {
			continue
		}
		k := metricLabels{platform: platformLabel(platform), namespace: tr.Namespace}
		if _, ok := wts[k]; !ok {
			wts[k] = .0
		}
		wts[k]++
	}
	for k, v := range wts {
		waitingTasksGauge.WithLabelValues(k.platform, k.namespace).Set(v)
	}
	log.V(1).Info("exported waiting tasks", "items", len(trList.Items))
	return nil
}
