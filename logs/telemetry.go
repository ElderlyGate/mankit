package logs

import (
	"context"
	"log/slog"

	"go.naywee.com/mankit/v5/features"
)

// Endpoint is the standard variable naming where telemetry is sent (ADR-E5).
const Endpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

// Telemetry is the telemetry feature for the app's list (ADR-E5): on, every record is also sent over OTLP to Endpoint, with the other standard OTEL_* variables.
// It starts off, as an enterprise feature (ADR-E9); on without Endpoint, the list says so as feature_needs_values, which the app's License and features section shows, and nothing is sent.
var Telemetry = features.Feature{ID: "telemetry", Title: "Telemetry", Covers: "logs sent over OpenTelemetry (OTLP) to a collector",
	Needs: []features.Value{{Name: Endpoint, Purpose: "where the collector takes logs over OTLP"}}, StartsOff: true}

// Exporter starts what sends records elsewhere, the telemetry feature's handler, and gives back the func that flushes and stops it; a nil handler sends nothing.
// An app that offers telemetry gets one from logs/otlp and hands it to its server (serve.Site.Telemetry), so an app without it links none of OpenTelemetry's modules.
type Exporter func(ctx context.Context) (slog.Handler, func(context.Context) error, error)
