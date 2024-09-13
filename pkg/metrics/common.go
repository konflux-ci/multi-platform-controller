package mpcmetrics

import "github.com/prometheus/client_golang/prometheus"

const (
	MetricsSubsystem = "multi_platform_controller"
	MetricsNamespace = "redhat_appstudio"
)

// ControllerAvailabilityGauge is a general service availability gauge. Provisioned by availability watcher.
// Must never hold values other than 0 or 1
var ControllerAvailabilityGauge = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Subsystem: MetricsSubsystem,
		Name:      "system_available",
		Help:      "The availability of the multi platform controller",
	})

func RegisterCommonMetrics(registerer prometheus.Registerer) error {
	registerer.MustRegister(ControllerAvailabilityGauge)
	return nil
}
