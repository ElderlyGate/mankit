// Package otlp sends an app's log records over OpenTelemetry (OTLP) when its telemetry feature is on (ADR-E5, E9): the one package that links OpenTelemetry's modules, so only an app that offers telemetry carries them.
package otlp

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/logs"
)

// For is the Exporter of the app id at version with list, for serve.Site.Telemetry.
func For(list *features.List, id, version string) logs.Exporter {
	return func(ctx context.Context) (slog.Handler, func(context.Context) error, error) {
		return Export(ctx, list, id, version)
	}
}

// Export is the handler that sends records over OTLP when the app's list has logs.Telemetry on with every value it needs, nil otherwise, and the func that flushes and stops it.
func Export(ctx context.Context, list *features.List, id, version string) (slog.Handler, func(context.Context) error, error) {
	none := func(context.Context) error { return nil }
	if list == nil || list.Known(logs.Telemetry.ID) != nil {
		return nil, none, nil
	}
	on, err := list.On(logs.Telemetry.ID)
	// On without its endpoint: the list says so (feature_needs_values), and nothing is sent.
	var missing *features.NeedsValues
	if errors.As(err, &missing) {
		return nil, none, nil
	}
	if err != nil || !on {
		return nil, none, err
	}
	exp, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, none, err
	}
	res := resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(id), semconv.ServiceVersion(version))
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)), sdklog.WithResource(res))
	return otelslog.NewHandler(id, otelslog.WithLoggerProvider(provider)), provider.Shutdown, nil
}
