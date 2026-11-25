//go:build !twouser

package store_test

import (
	"testing"

	"go.naywee.com/mankit/v5/internal/testhome"
)

// TestMain runs the tests in a home of their own (testhome.Run); the twouser steps run without it, as each keeps its journals in its container's home from one step to the next.
func TestMain(m *testing.M) { testhome.Run(m) }
