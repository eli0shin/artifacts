package telemetry

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

type Providers struct {
	traces *trace.TracerProvider
	logs   *log.LoggerProvider
}

func Setup(ctx context.Context, endpoint string) (*slog.Logger, *Providers, error) {
	serviceResource, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("published-artifact"),
	))
	if err != nil {
		return nil, nil, err
	}
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, nil, err
	}
	logExporter, err := otlploggrpc.New(ctx, otlploggrpc.WithEndpoint(endpoint), otlploggrpc.WithInsecure())
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		return nil, nil, err
	}

	traceProvider := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(serviceResource),
		trace.WithSampler(trace.AlwaysSample()),
	)
	logProvider := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
		log.WithResource(serviceResource),
	)
	otel.SetTracerProvider(traceProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	logger := otelslog.NewLogger("artifact-server", otelslog.WithLoggerProvider(logProvider))
	return logger, &Providers{traces: traceProvider, logs: logProvider}, nil
}

func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(p.logs.Shutdown(ctx), p.traces.Shutdown(ctx))
}
