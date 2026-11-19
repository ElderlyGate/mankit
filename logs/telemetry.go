package logs

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
)

// Endpoint is the standard variable naming where telemetry is sent (ADR-E5).
const Endpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

// Telemetry is the telemetry feature for the app's list (ADR-E5): on, every record is also sent over OTLP to Endpoint, with the other standard OTEL_* variables.
// It starts off, as an enterprise feature (ADR-E9); on without Endpoint, the list says so as feature_needs_values, which the app's License and features section shows, and nothing is sent.
var Telemetry = features.Feature{ID: "telemetry", Title: "Telemetry", Covers: "logs sent over OpenTelemetry (OTLP) to a collector",
	Needs: []features.Value{{Name: Endpoint, Purpose: "where the collector takes logs over OTLP"}}, StartsOff: true}

// Export is the handler that sends records over OTLP when the app's list has Telemetry on with every value it needs, nil otherwise, and the func that flushes and stops it.
func Export(ctx context.Context, list *features.List, id, version string) (slog.Handler, func(context.Context) error, error) {
	none := func(context.Context) error { return nil }
	if list == nil || list.Known(Telemetry.ID) != nil {
		return nil, none, nil
	}
	on, err := list.On(Telemetry.ID)
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
