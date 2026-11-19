package otlp_test

import (
	"context"
	"testing"

	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/logs"
	"github.com/tugbayatilla/mankit/v4/logs/otlp"
)

// The telemetry feature sends nothing off, or on without its endpoint, and sends once both are there (ADR-E5, E9).
func TestTelemetrySendsOnlyWhenOnWithItsEndpoint(t *testing.T) {
	ctx := context.Background()
	if h, _, err := otlp.Export(ctx, nil, "designer", "9.0.0"); h != nil || err != nil {
		t.Fatalf("no list: %v %v", h, err)
	}
	env := map[string]bool{}
	list := func(on bool) *features.List {
		ids := func() ([]string, error) {
			if on {
				return []string{logs.Telemetry.ID}, nil
			}
			return nil, nil
		}
		store := features.Store{Off: func() ([]string, error) { return nil, nil }, Keep: func(string, bool) error { return nil }, On: ids}
		return features.New([]features.Feature{logs.Telemetry}, store).WithLookup(func(name string) bool { return env[name] })
	}
	if h, _, err := otlp.Export(ctx, list(false), "designer", "9.0.0"); h != nil || err != nil {
		t.Fatalf("off: %v %v", h, err)
	}
	if h, _, err := otlp.Export(ctx, list(true), "designer", "9.0.0"); h != nil || err != nil {
		t.Fatalf("on without the endpoint: %v %v", h, err)
	}
	broken, _ := list(true).Broken()
	if len(broken) != 1 || broken[0].Missing[0].Name != logs.Endpoint {
		t.Fatalf("on without the endpoint is not said: %+v", broken)
	}
	env[logs.Endpoint] = true
	t.Setenv(logs.Endpoint, "http://127.0.0.1:4318")
	h, stop, err := otlp.Export(ctx, list(true), "designer", "9.0.0")
	if h == nil || err != nil {
		t.Fatalf("on with the endpoint: %v %v", h, err)
	}
	_ = stop(ctx)
}
