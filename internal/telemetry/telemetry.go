// Package telemetry connects zap logs, OpenTelemetry traces and Prometheus metrics.
package telemetry

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type requestKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestKey{}, id)
}

func Logger(ctx context.Context) *zap.Logger {
	sc := trace.SpanContextFromContext(ctx)
	log := zap.L()
	if sc.IsValid() {
		log = log.With(
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}
	if id, ok := ctx.Value(requestKey{}).(string); ok {
		log = log.With(zap.String("request_id", id))
	}
	return log
}

func NewLogger(level, process, version string) (*zap.Logger, error) {
	c := zap.NewProductionConfig()
	c.OutputPaths = []string{"stdout"}
	c.ErrorOutputPaths = []string{"stderr"}
	c.EncoderConfig.TimeKey = "timestamp"
	c.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	if err := c.Level.UnmarshalText([]byte(level)); err != nil {
		return nil, err
	}
	c.Sampling = nil // Every request remains searchable in the learning environment.
	log, err := c.Build(
		zap.Fields(zap.String("service", "payments-"+process), zap.String("version", version)),
	)
	if err == nil {
		zap.ReplaceGlobals(log)
	}
	return log, err
}

// An empty endpoint keeps trace correlation locally without creating an exporter.
func InitTracing(
	ctx context.Context,
	service, version, endpoint string,
) (func(context.Context) error, error) {
	res := resource.NewWithAttributes(
		"",
		attribute.String("service.name", service),
		attribute.String("service.version", version),
	)
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	}
	if endpoint != "" {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
		if err != nil {
			return nil, err
		}
		options = append(options, sdktrace.WithBatcher(
			exporter,
			sdktrace.WithBatchTimeout(time.Second),
			sdktrace.WithExportTimeout(3*time.Second),
		))
	}
	provider := sdktrace.NewTracerProvider(options...)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetErrorHandler(
		otel.ErrorHandlerFunc(func(err error) { zap.L().Warn("trace export failed", zap.Error(err)) }),
	)
	return provider.Shutdown, nil
}

func Start(ctx context.Context, name string) (context.Context, func(error)) {
	ctx, span := otel.Tracer("payments").Start(ctx, name)
	started := time.Now()
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		fields := []zap.Field{zap.String("operation", name), zap.Duration("duration", time.Since(started))}
		if err != nil {
			Logger(ctx).Warn("operation finished", append(fields, zap.Error(err))...)
		} else {
			Logger(ctx).Info("operation finished", fields...)
		}
		span.End()
	}
}

type Metrics struct {
	Registry      *prometheus.Registry
	Requests      *prometheus.CounterVec
	Duration      *prometheus.HistogramVec
	WorkerRuns    *prometheus.CounterVec
	WorkerSuccess prometheus.Gauge
	Expired       prometheus.Counter
}

func NewMetrics() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		Requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "payments_http_requests_total", Help: "Completed HTTP requests."},
			[]string{"method", "route", "status"},
		),
		Duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "payments_http_request_duration_seconds",
				Help:    "HTTP request latency.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "route"},
		),
		WorkerRuns: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "payments_worker_runs_total", Help: "Expiration passes."},
			[]string{"result"},
		),
		WorkerSuccess: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "payments_worker_last_success_timestamp_seconds",
				Help: "Last successful expiration pass.",
			},
		),
		Expired: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "payments_invoices_expired_total",
				Help: "Invoices expired by this worker.",
			},
		),
	}
	m.Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.Requests,
		m.Duration,
		m.WorkerRuns,
		m.WorkerSuccess,
		m.Expired,
	)
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
