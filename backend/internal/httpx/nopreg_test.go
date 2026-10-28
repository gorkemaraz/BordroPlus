package httpx

import "github.com/prometheus/client_golang/prometheus"

// nopRegisterer lets tests build Metrics without a global registry.
type nopRegisterer struct{}

func (nopRegisterer) Register(prometheus.Collector) error  { return nil }
func (nopRegisterer) MustRegister(...prometheus.Collector) {}
func (nopRegisterer) Unregister(prometheus.Collector) bool { return true }
