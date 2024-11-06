package mpcmetrics

import (
	"context"
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var _ = Describe("PlatformMetrics", func() {

	Describe("Gauges", func() {
		var (
			platform              = "ibm_p"
			runTasksMetricName    = "multi_platform_controller_running_tasks"
			waitingTaskMetricName = "multi_platform_controller_waiting_tasks"
			expectedValue         = 1
		)
		BeforeEach(func() {
			Expect(RegisterPlatformMetrics(context.TODO(), platform)).NotTo(HaveOccurred())
			//resetting counters
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.RunningTasks.Set(0)
			})
			HandleMetrics(platform, func(m *PlatformMetrics) {
				m.WaitingTasks.Set(0)
			})

		})
		When("When appropriate condition happened", func() {
			It("should increment running_tasks metric", func() {
				HandleMetrics(platform, func(m *PlatformMetrics) {
					m.RunningTasks.Inc()
				})
				result, err := getGaugeValue(platform, runTasksMetricName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(expectedValue))
			})

			It("should increment waiting_tasks metric", func() {
				HandleMetrics(platform, func(m *PlatformMetrics) {
					m.WaitingTasks.Inc()
				})
				result, err := getGaugeValue(platform, waitingTaskMetricName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(expectedValue))
			})
		})

	})

	Describe("Counters", func() {
		var (
			platform                         = "ibm_z"
			provisionFailuresMetricName      = "multi_platform_controller_provisioning_failures"
			cleanupFailuresMetricName        = "multi_platform_controller_cleanup_failures"
			hostAllocationFailuresMetricName = "multi_platform_controller_host_allocation_failures"
			expectedValue                    int
		)
		BeforeEach(func() {
			Expect(RegisterPlatformMetrics(context.TODO(), platform)).NotTo(HaveOccurred())
		})

		When("When appropriate condition happened", func() {
			It("should increment provisioning_failures metric", func() {
				HandleMetrics(platform, func(m *PlatformMetrics) {
					rnd := rand.Intn(100)
					expectedValue = rnd
					m.ProvisionFailures.Add(float64(rnd))
				})
				result, err := getCounterValue(platform, provisionFailuresMetricName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(expectedValue))
			})

			It("should increment cleanup_failures metric", func() {
				rnd := rand.Intn(100)
				expectedValue = rnd
				HandleMetrics(platform, func(m *PlatformMetrics) {
					m.CleanupFailures.Add(float64(rnd))
				})
				result, err := getCounterValue(platform, cleanupFailuresMetricName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(expectedValue))
			})

			It("should increment host_allocation metric", func() {
				rnd := rand.Intn(100)
				expectedValue = rnd
				HandleMetrics(platform, func(m *PlatformMetrics) {
					m.HostAllocationFailures.Add(float64(rnd))
				})
				result, err := getCounterValue(platform, hostAllocationFailuresMetricName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(expectedValue))
			})
		})

	})

	Describe("Histograms", func() {
		var (
			platform                 = "ibm_x"
			allocationTimeMetricName = "multi_platform_controller_host_allocation_time"
			waitTimeMetricName       = "multi_platform_controller_wait_time"
			taskRunMetricName        = "multi_platform_controller_task_run_time"
			expectedValue            float64
		)
		BeforeEach(func() {
			Expect(RegisterPlatformMetrics(context.TODO(), platform)).NotTo(HaveOccurred())
		})

		When("When appropriate condition happened", func() {
			It("should increment host_allocation metric", func() {
				rnd := rand.Float64()
				expectedValue = rnd
				HandleMetrics(platform, func(m *PlatformMetrics) {
					m.AllocationTime.Observe(rnd)
				})
				result, err := getHistogramValue(platform, allocationTimeMetricName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(expectedValue))
			})

			It("should increment wait_time metric", func() {
				rnd := rand.Float64()
				expectedValue = rnd
				HandleMetrics(platform, func(m *PlatformMetrics) {
					m.WaitTime.Observe(rnd)
				})
				result, err := getHistogramValue(platform, waitTimeMetricName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(expectedValue))
			})

			It("should increment task_run metric", func() {
				rnd := rand.Float64()
				expectedValue = rnd
				HandleMetrics(platform, func(m *PlatformMetrics) {
					m.TaskRunTime.Observe(rnd)
				})
				result, err := getHistogramValue(platform, taskRunMetricName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(expectedValue))
			})
		})

	})

})

func getGaugeValue(platform, metricName string) (int, error) {
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		return 0, err
	}
	for _, mf := range mfs {
		if mf.GetName() == metricName {
			for _, m := range mf.GetMetric() {
				if m.Gauge != nil && m.Label[0].GetValue() == platform {
					return int(m.Gauge.GetValue()), nil
				}
			}
		}
	}
	return 0, err
}

func getCounterValue(platform, metricName string) (int, error) {
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		return 0, err
	}
	for _, mf := range mfs {
		if mf.GetName() == metricName {
			for _, m := range mf.GetMetric() {
				if m.Counter != nil && m.Label[0].GetValue() == platform {
					return int(m.Counter.GetValue()), nil
				}
			}
		}
	}
	return 0, err
}

func getHistogramValue(platform, metricName string) (float64, error) {
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		return 0, err
	}
	for _, mf := range mfs {
		if mf.GetName() == metricName {
			for _, m := range mf.GetMetric() {
				if m.Histogram != nil && m.Label[0].GetValue() == platform {
					return *m.Histogram.SampleSum, nil
				}
			}
		}
	}
	return 0, err
}
