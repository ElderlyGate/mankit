package storewall_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/tugbayatilla/mankit/v3/internal/storewall"
)

// Every write and encode outside the homes is refused, reads are not, and an exception allows only what it names.
func TestTheWall(t *testing.T) {
	analysistest.Run(t, analysistest.TestData()+"/src/app", storewall.Analyzer, "./...")
}
