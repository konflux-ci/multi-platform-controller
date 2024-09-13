package mpcmetrics

import (
	"context"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"testing"
)

func TestRegisterPlatformMetricsGauges(t *testing.T) {

	var platform = "ibm_p"

	var tests = []struct {
		name          string
		resetFunc     func()
		incrementFunc func()
		metricName    string
		want          int
	}{
		{"running_tasks", func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.RunningTasks.Set(0)
			})
		}, func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.RunningTasks.Inc()
			})
		},
			"redhat_appstudio_multi_platform_controller_running_tasks", 1},

		{"waiting_tasks", func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.WaitingTasks.Set(0)
			})
		}, func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.WaitingTasks.Inc()
			})
		},
			"redhat_appstudio_multi_platform_controller_waiting_tasks", 1},
	}

	// register metrics
	assert.NoError(t, RegisterPlatformMetrics(context.TODO(), platform))

	// The execution loop
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.resetFunc()
			tt.incrementFunc()
			mfs, err := metrics.Registry.Gather()
			assert.NoError(t, err)
			for _, mf := range mfs {
				if mf.GetName() != tt.metricName {
					continue
				}
				for _, m := range mf.GetMetric() {
					if m.Gauge != nil && m.Label[0].GetValue() == platform {
						assert.Equal(t, tt.want, int(m.Gauge.GetValue()))
					}
				}
			}
		})
	}
}

func TestRegisterPlatformMetricsCounters(t *testing.T) {

	var platform = "ibm_z"

	var tests = []struct {
		name          string
		resetFunc     func()
		incrementFunc func()
		metricName    string
		want          int
	}{
		{"provisioning_failures", func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				//no way to reset counter
			})
		}, func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.ProvisionFailures.Inc()
			})
		},
			"redhat_appstudio_multi_platform_controller_provisioning_failures", 1},

		{"cleanup_failures", func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				//no way to reset counter
			})
		}, func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.CleanupFailures.Inc()
			})
		},
			"redhat_appstudio_multi_platform_controller_cleanup_failures", 1},

		{"host_allocation_failures", func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				//no way to reset counter
			})
		}, func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.HostAllocationFailures.Inc()
			})
		},
			"redhat_appstudio_multi_platform_controller_host_allocation_failures", 1},
	}

	assert.NoError(t, RegisterPlatformMetrics(context.TODO(), platform))

	// The execution loop
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.resetFunc()
			tt.incrementFunc()
			mfs, err := metrics.Registry.Gather()
			assert.NoError(t, err)
			for _, mf := range mfs {
				if mf.GetName() != tt.metricName {
					continue
				}
				for _, m := range mf.GetMetric() {
					if m.Counter != nil && m.Label[0].GetValue() == platform {
						assert.Equal(t, tt.want, int(m.Counter.GetValue()))
					}
				}
			}
		})
	}
}

func TestRegisterPlatformMetricsHistograms(t *testing.T) {

	var platform = "ibm_x"

	var tests = []struct {
		name          string
		resetFunc     func()
		incrementFunc func()
		metricName    string
		want          float64
	}{
		{"host_allocation_time", func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				//no way to reset histogram
			})
		}, func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.AllocationTime.Observe(1.1)
			})
		},
			"redhat_appstudio_multi_platform_controller_host_allocation_time", 1.1},

		{"wait_time", func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				//no way to reset histogram
			})
		}, func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.WaitTime.Observe(2.2)
			})
		},
			"redhat_appstudio_multi_platform_controller_wait_time", 2.2},

		{"task_run_time", func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				//no way to reset histogram
			})
		}, func() {
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.TaskRunTime.Observe(3.3)
			})
		},
			"redhat_appstudio_multi_platform_controller_task_run_time", 3.3},
	}

	assert.NoError(t, RegisterPlatformMetrics(context.TODO(), platform))

	// The execution loop
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.resetFunc()
			tt.incrementFunc()
			mfs, err := metrics.Registry.Gather()
			assert.NoError(t, err)
			for _, mf := range mfs {
				if mf.GetName() != tt.metricName {
					continue
				}
				for _, m := range mf.GetMetric() {
					if m.Histogram != nil && m.Label[0].GetValue() == platform {
						assert.Equal(t, tt.want, *m.Histogram.SampleSum)
					}
				}
			}
		})
	}
}
