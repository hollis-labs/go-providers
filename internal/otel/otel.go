package otel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// StartSpan starts a span using the provider package's local tracer scope.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return otel.Tracer("github.com/hollis-labs/go-providers/internal/otel").Start(ctx, name, opts...)
}
