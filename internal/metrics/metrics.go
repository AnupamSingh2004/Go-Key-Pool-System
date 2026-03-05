package metrics

import (
"fmt"
"net/http"
"sync"
"sync/atomic"
"time"
)

// Metrics holds Prometheus-style counters and gauges.
type Metrics struct {
// Request counters
RequestsSubmitted atomic.Int64
RequestsCompleted atomic.Int64
RequestsFailed    atomic.Int64
RequestsRetried   atomic.Int64

// Queue gauge
QueueSize atomic.Int64

// Worker gauge
ActiveWorkers atomic.Int64
TotalWorkers  atomic.Int64

// Key pool gauges
TotalKeys   atomic.Int64
HealthyKeys atomic.Int64

// HTTP response codes (status code → count)
mu          sync.RWMutex
StatusCodes map[int]*atomic.Int64

// Latency tracking
latencyMu     sync.Mutex
totalLatencyMs atomic.Int64
latencyCount   atomic.Int64

startTime time.Time
}

// New creates a new Metrics instance.
func New() *Metrics {
return &Metrics{
StatusCodes: make(map[int]*atomic.Int64),
startTime:   time.Now(),
}
}

// RecordStatusCode increments the counter for a given HTTP status code.
func (m *Metrics) RecordStatusCode(code int) {
m.mu.RLock()
counter, ok := m.StatusCodes[code]
m.mu.RUnlock()

if ok {
counter.Add(1)
return
}

m.mu.Lock()
// Double-check after acquiring write lock
if counter, ok = m.StatusCodes[code]; ok {
m.mu.Unlock()
counter.Add(1)
return
}
counter = &atomic.Int64{}
counter.Add(1)
m.StatusCodes[code] = counter
m.mu.Unlock()
}

// RecordLatency records a request latency in milliseconds.
func (m *Metrics) RecordLatency(ms int64) {
m.totalLatencyMs.Add(ms)
m.latencyCount.Add(1)
}

// Handler returns an HTTP handler that exposes metrics in Prometheus text format.
func (m *Metrics) Handler() http.HandlerFunc {
return func(w http.ResponseWriter, r *http.Request) {
w.Header().Set("Content-Type", "text/plain; charset=utf-8")

// Uptime
uptime := time.Since(m.startTime).Seconds()
fmt.Fprintf(w, "# HELP keypool_uptime_seconds Time since service started.\n")
fmt.Fprintf(w, "# TYPE keypool_uptime_seconds gauge\n")
fmt.Fprintf(w, "keypool_uptime_seconds %.2f\n\n", uptime)

// Requests
fmt.Fprintf(w, "# HELP keypool_requests_submitted_total Total requests submitted.\n")
fmt.Fprintf(w, "# TYPE keypool_requests_submitted_total counter\n")
fmt.Fprintf(w, "keypool_requests_submitted_total %d\n\n", m.RequestsSubmitted.Load())

fmt.Fprintf(w, "# HELP keypool_requests_completed_total Total requests completed successfully.\n")
fmt.Fprintf(w, "# TYPE keypool_requests_completed_total counter\n")
fmt.Fprintf(w, "keypool_requests_completed_total %d\n\n", m.RequestsCompleted.Load())

fmt.Fprintf(w, "# HELP keypool_requests_failed_total Total requests failed permanently.\n")
fmt.Fprintf(w, "# TYPE keypool_requests_failed_total counter\n")
fmt.Fprintf(w, "keypool_requests_failed_total %d\n\n", m.RequestsFailed.Load())

fmt.Fprintf(w, "# HELP keypool_requests_retried_total Total request retries.\n")
fmt.Fprintf(w, "# TYPE keypool_requests_retried_total counter\n")
fmt.Fprintf(w, "keypool_requests_retried_total %d\n\n", m.RequestsRetried.Load())

// Queue
fmt.Fprintf(w, "# HELP keypool_queue_size Current queue depth.\n")
fmt.Fprintf(w, "# TYPE keypool_queue_size gauge\n")
fmt.Fprintf(w, "keypool_queue_size %d\n\n", m.QueueSize.Load())

// Workers
fmt.Fprintf(w, "# HELP keypool_workers_active Currently active workers.\n")
fmt.Fprintf(w, "# TYPE keypool_workers_active gauge\n")
fmt.Fprintf(w, "keypool_workers_active %d\n\n", m.ActiveWorkers.Load())

fmt.Fprintf(w, "# HELP keypool_workers_total Total worker count.\n")
fmt.Fprintf(w, "# TYPE keypool_workers_total gauge\n")
fmt.Fprintf(w, "keypool_workers_total %d\n\n", m.TotalWorkers.Load())

// Keys
fmt.Fprintf(w, "# HELP keypool_keys_total Total API keys in pool.\n")
fmt.Fprintf(w, "# TYPE keypool_keys_total gauge\n")
fmt.Fprintf(w, "keypool_keys_total %d\n\n", m.TotalKeys.Load())

fmt.Fprintf(w, "# HELP keypool_keys_healthy Healthy API keys in pool.\n")
fmt.Fprintf(w, "# TYPE keypool_keys_healthy gauge\n")
fmt.Fprintf(w, "keypool_keys_healthy %d\n\n", m.HealthyKeys.Load())

// Status codes
m.mu.RLock()
if len(m.StatusCodes) > 0 {
fmt.Fprintf(w, "# HELP keypool_http_response_status_total HTTP response status codes from upstream.\n")
fmt.Fprintf(w, "# TYPE keypool_http_response_status_total counter\n")
for code, counter := range m.StatusCodes {
fmt.Fprintf(w, "keypool_http_response_status_total{code=\"%d\"} %d\n", code, counter.Load())
}
fmt.Fprintln(w)
}
m.mu.RUnlock()

// Latency
count := m.latencyCount.Load()
if count > 0 {
avg := float64(m.totalLatencyMs.Load()) / float64(count)
fmt.Fprintf(w, "# HELP keypool_request_latency_avg_ms Average request latency in milliseconds.\n")
fmt.Fprintf(w, "# TYPE keypool_request_latency_avg_ms gauge\n")
fmt.Fprintf(w, "keypool_request_latency_avg_ms %.2f\n", avg)
}
}
}
