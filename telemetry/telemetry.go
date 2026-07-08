package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Setup installs the OTel globals from cfg and returns a shutdown that flushes
// and closes the providers. It ALWAYS installs the W3C TraceContext+Baggage
// propagator and a TracerProvider that assigns a trace-context to every request;
// OTLP exporters are wired ONLY when otlp.endpoint is set. Fails fast on invalid
// exporter config; a down collector is not an error (lazy connect + retry).
//
// Setup is call-once, at boot: calling it again without first invoking the
// shutdown returned by the previous call orphans that call's providers (leaked
// goroutines/connections), since Setup does not shut down any prior globals.
func Setup(ctx context.Context, cfg Config, logger *slog.Logger) (func(context.Context) error, error) {
	if logger == nil {
		logger = slog.Default()
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("otel", "err", err) // pipeline/exporter errors only; never a token
	}))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))

	tp, mp, shutdown, err := build(ctx, cfg)
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	return shutdown, nil
}

// build constructs the providers without touching globals (unit-testable). On a
// construction error after a provider is built, it rolls back (shuts down) the
// already-built providers so no goroutine/exporter connection leaks.
func build(ctx context.Context, cfg Config) (tp *sdktrace.TracerProvider, mp *metric.MeterProvider, shutdown func(context.Context) error, err error) {
	if cfg.OTLP.Endpoint != "" && cfg.OTLP.Protocol != protoHTTP && cfg.OTLP.Protocol != protoGRPC {
		return nil, nil, nil, fmt.Errorf("telemetry: otlp.protocol %q", cfg.OTLP.Protocol)
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(cfg.Service.Name),
		semconv.ServiceVersion(cfg.Service.Version),
	))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("telemetry: resource: %w", err)
	}
	var timeout time.Duration
	if cfg.OTLP.Timeout != "" {
		if timeout, err = time.ParseDuration(cfg.OTLP.Timeout); err != nil {
			return nil, nil, nil, fmt.Errorf("telemetry: otlp.timeout: %w", err)
		}
	}
	exporting := cfg.OTLP.Endpoint != ""

	var shutdowns []func(context.Context) error
	defer func() {
		if err != nil {
			for _, s := range shutdowns {
				_ = s(context.Background()) // best-effort rollback
			}
		}
	}()

	root := sdktrace.NeverSample()
	if exporting {
		root = sdktrace.TraceIDRatioBased(cfg.Sampling.Ratio)
	}
	traceOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(root)),
	}
	if exporting {
		texp, terr := traceExporter(ctx, cfg.OTLP, timeout)
		if terr != nil {
			err = fmt.Errorf("telemetry: trace exporter: %w", terr)
			return nil, nil, nil, err
		}
		traceOpts = append(traceOpts, sdktrace.WithBatcher(texp))
	}
	tp = sdktrace.NewTracerProvider(traceOpts...)
	shutdowns = append(shutdowns, tp.Shutdown)

	metricOpts := []metric.Option{metric.WithResource(res)}
	if exporting {
		mexp, merr := metricExporter(ctx, cfg.OTLP, timeout)
		if merr != nil {
			err = fmt.Errorf("telemetry: metric exporter: %w", merr)
			return nil, nil, nil, err
		}
		metricOpts = append(metricOpts, metric.WithReader(metric.NewPeriodicReader(mexp)))
	}
	mp = metric.NewMeterProvider(metricOpts...) // no reader ⇒ recordings dropped (no export)
	shutdowns = append(shutdowns, mp.Shutdown)

	shutdown = func(ctx context.Context) error {
		var first error
		for _, s := range shutdowns {
			if e := s(ctx); e != nil && first == nil {
				first = e
			}
		}
		return first
	}
	return tp, mp, shutdown, nil
}

func traceExporter(ctx context.Context, o OTLPConfig, timeout time.Duration) (sdktrace.SpanExporter, error) {
	if o.Protocol == protoGRPC {
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpointURL(o.Endpoint), otlptracegrpc.WithHeaders(o.Headers)}
		if o.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if timeout > 0 {
			opts = append(opts, otlptracegrpc.WithTimeout(timeout))
		}
		return otlptracegrpc.New(ctx, opts...)
	}
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(o.Endpoint), otlptracehttp.WithHeaders(o.Headers)}
	if o.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if timeout > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(timeout))
	}
	return otlptracehttp.New(ctx, opts...)
}

func metricExporter(ctx context.Context, o OTLPConfig, timeout time.Duration) (metric.Exporter, error) {
	if o.Protocol == protoGRPC {
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpointURL(o.Endpoint), otlpmetricgrpc.WithHeaders(o.Headers)}
		if o.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		if timeout > 0 {
			opts = append(opts, otlpmetricgrpc.WithTimeout(timeout))
		}
		return otlpmetricgrpc.New(ctx, opts...)
	}
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(o.Endpoint), otlpmetrichttp.WithHeaders(o.Headers)}
	if o.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	if timeout > 0 {
		opts = append(opts, otlpmetrichttp.WithTimeout(timeout))
	}
	return otlpmetrichttp.New(ctx, opts...)
}
