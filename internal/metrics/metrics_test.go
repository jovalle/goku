package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestRegister(t *testing.T) {
	reg := prometheus.NewRegistry()
	collectors := []prometheus.Collector{
		RedirectsTotal,
		RequestDuration,
		ResolveErrors,
		ConfigReloads,
		AliasesTotal,
	}
	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				t.Fatalf("failed to register collector: %v", err)
			}
		}
	}
}

func TestMetrics_Increment(t *testing.T) {
	RedirectsTotal.WithLabelValues("test-link").Inc()
	RequestDuration.WithLabelValues("GET", "200").Observe(0.01)
	ResolveErrors.Inc()
	ConfigReloads.Inc()
	AliasesTotal.Set(5)
}
