package mpcmetrics

import (
	"context"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (

	// Map of metrics set. Holds pointers, so no real need to be thread-safe here as the values are never rewritten.
	platformMetrics = map[string]*PlatformMetrics{}

	smallBuckets = []float64{1, 2, 3, 4, 5, 10, 15, 20, 30, 60, 120, 300, 600, 1200}
	bigBuckets   = []float64{20, 40, 60, 90, 120, 300, 600, 1200, 2400, 4800, 6000, 7200, 8400, 9600}
)

// PlatformMetrics set of per-platform metrics
type PlatformMetrics struct {
	AllocationTime         prometheus.Histogram
	WaitTime               prometheus.Histogram
	TaskRunTime            prometheus.Histogram
	RunningTasks           *prometheus.GaugeVec
	WaitingTasks           *prometheus.GaugeVec
	ProvisionFailures      prometheus.Counter
	CleanupFailures        prometheus.Counter
	HostAllocationFailures prometheus.Counter
	poolSize               *prometheus.GaugeVec // package-private to avoid modifications
}

func RegisterPlatformMetrics(_ context.Context, platform string, poolSize int) error {
	platform = platformLabel(platform)
	if _, ok := platformMetrics[platformLabel(platform)]; ok {
		return nil
	}
	pmetrics := PlatformMetrics{}

	pmetrics.AllocationTime = prometheus.NewHistogram(prometheus.HistogramOpts{
		ConstLabels: map[string]string{"platform": platform},
		Subsystem:   MetricsSubsystem,
		Name:        "host_allocation_time",
		Help:        "The time in seconds it takes to allocate a host, excluding wait time. In practice this is the amount of time it takes a cloud provider to start an instance",
		Buckets:     smallBuckets})
	if err := metrics.Registry.Register(pmetrics.AllocationTime); err != nil {
		return err
	}

	pmetrics.WaitTime = prometheus.NewHistogram(prometheus.HistogramOpts{
		ConstLabels: map[string]string{"platform": platform},
		Subsystem:   MetricsSubsystem,
		Name:        "wait_time",
		Help:        "The time in seconds a task has spent waiting for a host to become available, excluding the host allocation time",
		Buckets:     smallBuckets})
	if err := metrics.Registry.Register(pmetrics.WaitTime); err != nil {
		return err
	}

	pmetrics.TaskRunTime = prometheus.NewHistogram(prometheus.HistogramOpts{
		ConstLabels: map[string]string{"platform": platform},
		Subsystem:   MetricsSubsystem,
		Name:        "task_run_time",
		Help:        "The total time taken by a task, including wait and allocation time",
		Buckets:     bigBuckets})
	if err := metrics.Registry.Register(pmetrics.TaskRunTime); err != nil {
		return err
	}

	pmetrics.RunningTasks = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: MetricsSubsystem,
		Name:      "running_tasks",
		Help:      "The number of currently running tasks on this platform",
	}, []string{"platform", "taskrun_namespace"})
	if err := metrics.Registry.Register(pmetrics.RunningTasks); err != nil {
		// This can be called multiple times, so we need to check if it's already registered
		if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
			return err
		}
	}

	pmetrics.WaitingTasks = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: MetricsSubsystem,
		Name:      "waiting_tasks",
		Help:      "The number of tasks waiting for an executor to be available to run",
	}, []string{"platform", "taskrun_namespace"})
	if err := metrics.Registry.Register(pmetrics.WaitingTasks); err != nil {
		if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
			return err
		}
	}

	pmetrics.ProvisionFailures = prometheus.NewCounter(prometheus.CounterOpts{
		ConstLabels: map[string]string{"platform": platform},
		Subsystem:   MetricsSubsystem,
		Name:        "provisioning_failures",
		Help:        "The number of times a provisioning task has failed"})
	if err := metrics.Registry.Register(pmetrics.ProvisionFailures); err != nil {
		return err
	}

	pmetrics.CleanupFailures = prometheus.NewCounter(prometheus.CounterOpts{
		ConstLabels: map[string]string{"platform": platform},
		Subsystem:   MetricsSubsystem,
		Name:        "cleanup_failures",
		Help:        "The number of times a cleanup task has failed"})
	if err := metrics.Registry.Register(pmetrics.CleanupFailures); err != nil {
		return err
	}

	pmetrics.HostAllocationFailures = prometheus.NewCounter(prometheus.CounterOpts{
		ConstLabels: map[string]string{"platform": platform},
		Subsystem:   MetricsSubsystem,
		Name:        "host_allocation_failures",
		Help:        "The number of times host allocation has failed"})
	if err := metrics.Registry.Register(pmetrics.HostAllocationFailures); err != nil {
		return err
	}

	pmetrics.poolSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: MetricsSubsystem,
		Name:      "platform_pool_size",
		Help:      "The size of platform machines pool",
	}, []string{"platform"})
	if err := metrics.Registry.Register(pmetrics.poolSize); err != nil {
		if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
			return err
		}
	}
	pmetrics.poolSize.WithLabelValues(platform).Set(float64(poolSize))
	platformMetrics[platform] = &pmetrics
	return nil
}

// Convert the platform label to the format used by PlatformMetrics in case of a mismatch
func platformLabel(platform string) string {
	return strings.ReplaceAll(platform, "/", "-")
}

func HandleMetrics(platform string, f func(*PlatformMetrics)) {
	platform = platformLabel(platform)
	if pmetrics := platformMetrics[platform]; pmetrics != nil {
		f(pmetrics)
	}
}
