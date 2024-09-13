package mpcmetrics

import (
	"github.com/prometheus/client_golang/prometheus"
	prometheusTest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestRegisterCommonMetrics(t *testing.T) {

	var tests = []struct {
		name          string
		resetFunc     func()
		incrementFunc func()
		metricName    string
		want          int
	}{
		{"availability gauge", func() {
			ControllerAvailabilityGauge.Set(0)
		}, func() {
			ControllerAvailabilityGauge.Inc()
		}, "redhat_appstudio_multi_platform_controller_system_available", 1},
	}
	// The execution loop
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := prometheus.NewPedanticRegistry()
			tt.resetFunc()
			assert.NoError(t, RegisterCommonMetrics(registry))
			tt.incrementFunc()
			count, err := prometheusTest.GatherAndCount(registry, tt.metricName)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, count)
		})
	}
}
