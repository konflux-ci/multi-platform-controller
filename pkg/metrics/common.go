package mpcmetrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	MetricsSubsystem = "multi_platform_controller"
)

var (
	probes                      = map[string]*AvailabilityProbe{}
	probesMutex                 = &sync.RWMutex{} // Read-Write mutex for probes
	controllerAvailabilityGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: MetricsSubsystem,
			Name:      "available",
			Help:      "The availability of the multi platform controller",
		},
		[]string{"check"})
)

func RegisterCommonMetrics(ctx context.Context, registerer prometheus.Registerer) error {
	log := logr.FromContextOrDiscard(ctx)
	if err := registerer.Register(controllerAvailabilityGauge); err != nil {
		return fmt.Errorf("failed to register the availability metric: %w", err)
	}
	ticker := time.NewTicker(5 * time.Minute)
	log.Info("Starting multi platform controller metrics")
	go func() {
		for {
			select {
			case <-ctx.Done(): // Shutdown if context is canceled
				log.Info("Shutting down MPC metrics")
				ticker.Stop()
				return
			case <-ticker.C:
				checkProbes(ctx)
			}
		}
	}()
	return nil
}

func checkProbes(ctx context.Context) {
	probesMutex.RLock() // Guard against a race condition when approaching probes for non-exclusive access
	defer probesMutex.RUnlock()

	log := logr.FromContextOrDiscard(ctx)
	for platform, probe := range probes {
		checkLabel := prometheus.Labels{"check": platform}
		checkErr := (*probe).CheckAvailability(ctx)
		if checkErr != nil {
			log.Error(checkErr, "Failing availability probe due to high error threshold", "platform", platform)
			controllerAvailabilityGauge.With(checkLabel).Set(0)
		} else {
			log.Info("Availability probe OK", "platform", platform)
			controllerAvailabilityGauge.With(checkLabel).Set(1)
		}
	}
}

func CountAvailabilitySuccess(platform string) {
	probesMutex.Lock() // Use a full lock for exclusive write access to guard against race conditions
	defer probesMutex.Unlock()

	if probes[platform] == nil {
		watcher := NewBackendProbe()
		probes[platform] = &watcher
	}
	(*probes[platform]).Success()
}

func CountAvailabilityError(platform string) {
	probesMutex.Lock() // Same exclusive lock as in CountAvailabilitySuccess
	defer probesMutex.Unlock()

	if probes[platform] == nil {
		watcher := NewBackendProbe()
		probes[platform] = &watcher
	}
	(*probes[platform]).Failure()
}

// AvailabilityProbe represents a probe that checks the availability of a certain aspects of the service
type AvailabilityProbe interface {
	CheckAvailability(ctx context.Context) error
	Success()
	Failure()
}
