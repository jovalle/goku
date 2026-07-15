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

	Register()
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
		"goku_config_reloads_total",
		"goku_redirects_total",
		"goku_request_duration_seconds",
		"goku_resolve_errors_total",
	} {
		if !got[name] {
			t.Errorf("metric %q was not registered", name)
		}
	}
}
