// Command storewall is the storage wall (ADR-S22) every app's CI runs: it fails a file written, or YAML or JSON encoded, outside mankit's store.
//
// In mankit:
//
//	go run ./cmd/storewall ./...
//
// In an app, at the mankit version the app is built with:
//
//	go run github.com/tugbayatilla/mankit/v3/cmd/storewall@$(go list -m -f '{{.Version}}' github.com/tugbayatilla/mankit/v3) ./...
//
// The app's exceptions, each with why, go in .storewall beside its go.mod (see internal/storewall).
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/tugbayatilla/mankit/v3/internal/storewall"
)

func main() { singlechecker.Main(storewall.Analyzer) }
