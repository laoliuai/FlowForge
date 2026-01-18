package metrics

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

const (
	customMetricPrefix = "flowforge_custom_"
)

type MetricEntry struct {
	Timestamp  int64             `json:"timestamp"`
	TaskID     string            `json:"task_id"`
	WorkflowID string            `json:"workflow_id"`
	TenantID   string            `json:"tenant_id"`
	Name       string            `json:"name"`
	Value      float64           `json:"value"`
	Type       string            `json:"type"`
	Tags       map[string]string `json:"tags"`
}

type metricBatch struct {
	Metrics []MetricEntry `json:"metrics"`
}

type customMetric struct {
	metricType string
	labels     []string
	counter    *prometheus.CounterVec
	gauge      *prometheus.GaugeVec
	histogram  *prometheus.HistogramVec
}

type Collector struct {
	logger *zap.Logger

	mu      sync.RWMutex
	metrics map[string]*customMetric

	ingestRequests      *prometheus.CounterVec
	ingestSamples       *prometheus.CounterVec
	ingestBatchSize     prometheus.Histogram
	ingestLatency       prometheus.Histogram
	ingestInvalid       *prometheus.CounterVec
	lastIngestTimestamp *prometheus.GaugeVec
}

func NewCollector(logger *zap.Logger) *Collector {
	collector := &Collector{
		logger:  logger,
		metrics: make(map[string]*customMetric),
		ingestRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "flowforge_metrics_ingest_requests_total",
				Help: "Total number of metrics ingest requests.",
			},
			[]string{"status"},
		),
		ingestSamples: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "flowforge_metrics_ingest_samples_total",
				Help: "Total number of metric samples ingested by type.",
			},
			[]string{"type"},
		),
		ingestBatchSize: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "flowforge_metrics_ingest_batch_size",
				Help:    "Batch size of metrics ingest requests.",
				Buckets: prometheus.ExponentialBuckets(1, 2, 12),
			},
		),
		ingestLatency: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "flowforge_metrics_ingest_latency_seconds",
				Help:    "Latency for processing metrics ingest requests.",
				Buckets: prometheus.DefBuckets,
			},
		),
		ingestInvalid: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "flowforge_metrics_ingest_invalid_total",
				Help: "Total number of invalid metric samples received.",
			},
			[]string{"reason"},
		),
		lastIngestTimestamp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "flowforge_metrics_last_ingest_timestamp_ms",
				Help: "Last ingest timestamp (ms since epoch) by tenant.",
			},
			[]string{"tenant_id"},
		),
	}

	prometheus.MustRegister(
		collector.ingestRequests,
		collector.ingestSamples,
		collector.ingestBatchSize,
		collector.ingestLatency,
		collector.ingestInvalid,
		collector.lastIngestTimestamp,
	)

	return collector
}

func (c *Collector) HandleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	defer func() {
		c.ingestLatency.Observe(time.Since(start).Seconds())
	}()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))

	var payload metricBatch
	if err := decoder.Decode(&payload); err != nil {
		c.ingestRequests.WithLabelValues("error").Inc()
		c.ingestInvalid.WithLabelValues("invalid_json").Inc()
		c.logger.Warn("failed to decode metrics payload", zap.Error(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if len(payload.Metrics) == 0 {
		c.ingestRequests.WithLabelValues("empty").Inc()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	c.ingestBatchSize.Observe(float64(len(payload.Metrics)))

	var invalidCount int
	for _, entry := range payload.Metrics {
		if err := c.applyMetric(entry); err != nil {
			invalidCount++
			c.ingestInvalid.WithLabelValues(err.Error()).Inc()
		} else {
			c.ingestSamples.WithLabelValues(entry.Type).Inc()
		}
	}

	if invalidCount > 0 {
		c.ingestRequests.WithLabelValues("partial").Inc()
	} else {
		c.ingestRequests.WithLabelValues("ok").Inc()
	}

	w.WriteHeader(http.StatusAccepted)
}

func (c *Collector) applyMetric(entry MetricEntry) error {
	if entry.Name == "" {
		return errors.New("missing_name")
	}
	if entry.Type == "" {
		return errors.New("missing_type")
	}
	if entry.Value != entry.Value {
		return errors.New("invalid_value")
	}

	metricType := strings.ToLower(entry.Type)
	switch metricType {
	case "counter":
	case "gauge":
	case "histogram":
	default:
		return errors.New("unknown_type")
	}

	metricName := customMetricPrefix + sanitizeMetricName(entry.Name)
	labels, labelValues := sanitizeTags(entry.Tags)
	custom := c.getOrCreateMetric(metricName, metricType, labels)

	if entry.TenantID != "" {
		timestamp := entry.Timestamp
		if timestamp == 0 {
			timestamp = time.Now().UnixMilli()
		}
		c.lastIngestTimestamp.WithLabelValues(entry.TenantID).Set(float64(timestamp))
	}

	switch metricType {
	case "counter":
		if entry.Value < 0 {
			return errors.New("negative_counter")
		}
		custom.counter.WithLabelValues(labelValues...).Add(entry.Value)
	case "gauge":
		custom.gauge.WithLabelValues(labelValues...).Set(entry.Value)
	case "histogram":
		custom.histogram.WithLabelValues(labelValues...).Observe(entry.Value)
	default:
		return errors.New("unknown_type")
	}

	return nil
}

func (c *Collector) getOrCreateMetric(name, metricType string, labels []string) *customMetric {
	key := name + "|" + strings.ToLower(metricType) + "|" + strings.Join(labels, ",")

	c.mu.RLock()
	if metric, ok := c.metrics[key]; ok {
		c.mu.RUnlock()
		return metric
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if metric, ok := c.metrics[key]; ok {
		return metric
	}

	metric := &customMetric{
		metricType: strings.ToLower(metricType),
		labels:     labels,
	}

	switch metric.metricType {
	case "counter":
		metric.counter = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: name,
				Help: "Custom counter metric from FlowForge tasks.",
			},
			labels,
		)
		prometheus.MustRegister(metric.counter)
	case "gauge":
		metric.gauge = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: name,
				Help: "Custom gauge metric from FlowForge tasks.",
			},
			labels,
		)
		prometheus.MustRegister(metric.gauge)
	case "histogram":
		metric.histogram = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    name,
				Help:    "Custom histogram metric from FlowForge tasks.",
				Buckets: prometheus.DefBuckets,
			},
			labels,
		)
		prometheus.MustRegister(metric.histogram)
	}

	c.metrics[key] = metric
	return metric
}

func sanitizeMetricName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown"
	}

	var b strings.Builder
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == ':' || (r >= '0' && r <= '9' && i > 0) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	sanitized := strings.ToLower(b.String())
	if sanitized == "" {
		return "unknown"
	}
	if sanitized[0] >= '0' && sanitized[0] <= '9' {
		return "_" + sanitized
	}
	return sanitized
}

func sanitizeTags(tags map[string]string) ([]string, []string) {
	if len(tags) == 0 {
		return nil, nil
	}

	type label struct {
		name  string
		value string
	}

	labels := make([]label, 0, len(tags))
	nameCounts := make(map[string]int)
	for key, value := range tags {
		name := sanitizeLabelName(key)
		if count, ok := nameCounts[name]; ok {
			nameCounts[name] = count + 1
			name = name + "_" + strconv.Itoa(count+1)
		} else {
			nameCounts[name] = 1
		}
		labels = append(labels, label{name: name, value: value})
	}

	sort.Slice(labels, func(i, j int) bool {
		return labels[i].name < labels[j].name
	})

	labelNames := make([]string, len(labels))
	labelValues := make([]string, len(labels))
	for i, item := range labels {
		labelNames[i] = item.name
		labelValues[i] = item.value
	}

	return labelNames, labelValues
}

func sanitizeLabelName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "label"
	}

	var b strings.Builder
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (r >= '0' && r <= '9' && i > 0) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}

	sanitized := strings.ToLower(b.String())
	if sanitized == "" {
		return "label"
	}
	if sanitized[0] >= '0' && sanitized[0] <= '9' {
		return "_" + sanitized
	}
	return sanitized
}
