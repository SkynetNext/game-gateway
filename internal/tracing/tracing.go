package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	// Tracer is the global tracer instance
	Tracer trace.Tracer

	// tracerProvider holds the tracer provider for shutdown
	tracerProvider *tracesdk.TracerProvider
)

// Init initializes OpenTelemetry tracing with OTLP exporter
// endpoint: OTel Collector gRPC endpoint (e.g., "otel-collector:4317")
func Init(serviceName, serviceVersion, endpoint string) error {
	if endpoint == "" {
		// Tracing disabled if no endpoint provided
		return nil
	}

	ctx := context.Background()

	// Create OTLP gRPC exporter
	// Connects to OTel Collector which routes to Tempo
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(), // Use TLS in production
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Get Pod info from environment (K8s downward API)
	podName := os.Getenv("POD_NAME")
	podNamespace := os.Getenv("POD_NAMESPACE")
	nodeName := os.Getenv("NODE_NAME")

	// Build resource attributes following OTel Semantic Conventions
	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
	}

	// Add K8s attributes if available
	if podName != "" {
		attrs = append(attrs, semconv.ServiceInstanceID(podName))
	}
	if podNamespace != "" {
		attrs = append(attrs, semconv.ServiceNamespace(podNamespace))
	}
	if nodeName != "" {
		attrs = append(attrs, semconv.K8SNodeName(nodeName))
	}

	// Create resource
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, attrs...),
	)
	if err != nil {
		// Fallback to simple resource
		res = resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		)
	}

	// Create tracer provider with batching for performance
	tracerProvider = tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exporter),
		tracesdk.WithResource(res),
		// Always sample 100% of traces (no sampling)
		tracesdk.WithSampler(tracesdk.AlwaysSample()),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tracerProvider)

	// Set W3C Trace Context propagator (standard for distributed tracing)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // W3C Trace Context
		propagation.Baggage{},      // W3C Baggage
	))

	Tracer = otel.Tracer(serviceName)

	return nil
}

// StartSpan starts a new span
func StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	if Tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return Tracer.Start(ctx, name)
}

// Shutdown gracefully shuts down the tracer provider
func Shutdown(ctx context.Context) error {
	if tracerProvider != nil {
		return tracerProvider.Shutdown(ctx)
	}
	return nil
}
