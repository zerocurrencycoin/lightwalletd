package common

import "github.com/prometheus/client_golang/prometheus"

// PrometheusMetrics is a list of collected Prometheus Counters and Guages that will be exported
type PrometheusMetrics struct {
	LatestBlockCounter      prometheus.Counter
	TotalBlocksServedConter prometheus.Counter
	SendTransactionsCounter prometheus.Counter
	TotalErrors             prometheus.Counter
}

func GetPrometheusMetrics() *PrometheusMetrics {
	m := &PrometheusMetrics{}
	m.LatestBlockCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lightwalletd_get_latest_block",
		Help: "Number of times GetLatestBlock was called",
	})

	m.TotalBlocksServedConter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lightwalletd_total_blocks_served",
		Help: "Total number of blocks served by lightwalletd",
	})

	m.SendTransactionsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lightwalletd_total_send_transactions",
		Help: "Total number of transactions broadcasted by lightwalletd",
	})

	m.TotalErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lightwalletd_total_errors",
		Help: "Total number of errors seen by lightwalletd",
	})

	return m
}