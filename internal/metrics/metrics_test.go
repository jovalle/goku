package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestRegister(t *testing.T) {
	registry := prometheus.NewRegistry()
	previousRegisterer := prometheus.DefaultRegisterer
	prometheus.DefaultRegisterer = registry
	t.Cleanup(func() {
		prometheus.DefaultRegisterer = previousRegisterer
	})

	Register(func() float64 { return 2 })
	RedirectsTotal.WithLabelValues("test")
	RequestDuration.WithLabelValues("GET", "200")

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(families))
	for _, family := range families {
		got[family.GetName()] = true
	}
	for _, name := range []string{
		"goku_aliases_configured",
		"goku_redirects_total",
		"goku_request_duration_seconds",
	} {
		if !got[name] {
			t.Errorf("metric %q was not registered", name)
		}
	}
	for _, family := range families {
		if family.GetName() == "goku_aliases_configured" && family.GetMetric()[0].GetGauge().GetValue() != 2 {
			t.Errorf("goku_aliases_configured = %v, want 2", family.GetMetric()[0].GetGauge().GetValue())
		}
	}
}
