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
)

// Register registers all goku metrics with Prometheus.
func Register(aliasCount func() float64) {
	prometheus.MustRegister(
		RedirectsTotal,
		RequestDuration,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "goku_aliases_configured",
			Help: "Current number of configured aliases.",
		}, aliasCount),
	)
}
