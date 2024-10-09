package mpcmetrics

import "github.com/prometheus/client_golang/prometheus"

const (
	MetricsSubsystem = "multi_platform_controller"
)

// ControllerAvailabilityGauge is a general service availability gauge. Provisioned by availability watcher.
// Must never hold values other than 0 or 1
var ControllerAvailabilityGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Subsystem: MetricsSubsystem,
		Name:      "available",
		Help:      "The availability of the multi platform controller",
	},
	[]string{"platform"})

func RegisterCommonMetrics(registerer prometheus.Registerer) error {
	registerer.MustRegister(ControllerAvailabilityGauge)
	return nil
}
