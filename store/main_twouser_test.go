//go:build twouser

package store_test

import (
	"fmt"
	"os"
	"testing"
)

// TestMain of the twouser build, a tag only testdata/twouser.sh uses: each step runs in a throwaway container whose home it keeps for the next step, so it refuses to run anywhere STORE_TWOUSER_ROLE is not set, such as a hand run against the real home.
func TestMain(m *testing.M) {
	if os.Getenv("STORE_TWOUSER_ROLE") == "" {
		fmt.Fprintln(os.Stderr, "the twouser tag is only for testdata/twouser.sh, which sets STORE_TWOUSER_ROLE in each step's container")
		os.Exit(2)
	}
	os.Exit(m.Run())
}
