package cli

import (
	"testing"

	"github.com/tugbayatilla/mankit/v4/internal/testhome"
)

// TestMain runs the tests in a home of their own (testhome.Run).
func TestMain(m *testing.M) { testhome.Run(m) }
