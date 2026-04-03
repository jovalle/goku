package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// RedirectsTotal counts all redirects by short name.
	RedirectsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goku_redirects_total",
			Help: "Total number of redirects.",
		},
		[]string{"short_name"},
	)

	// RequestDuration tracks HTTP request latency.
	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "goku_request_duration_seconds",
			Help:    "HTTP request duration.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
		},
		[]string{"method", "status"},
	)

	// ResolveErrors counts resolution failures.
	ResolveErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goku_resolve_errors_total",
		Help: "Total number of failed path resolutions.",
	})

	// ConfigReloads counts config file reloads.
	ConfigReloads = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "goku_config_reloads_total",
		Help: "Total number of config reloads.",
	})

	// AliasesTotal shows the current alias count.
	AliasesTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "goku_aliases_configured",
		Help: "Current number of configured aliases.",
	})

	// Backward-compatible aliases; all now mirror aliases count.
	LinksTotal = AliasesTotal
	RulesTotal = AliasesTotal
)

// Register registers all goku metrics with Prometheus.
func Register() {
	prometheus.MustRegister(
		RedirectsTotal,
		RequestDuration,
		ResolveErrors,
		ConfigReloads,
		AliasesTotal,
	)
}
