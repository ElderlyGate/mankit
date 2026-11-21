package build

import "testing"

func TestOnlyAStampedReleaseTagIsReleased(t *testing.T) {
	t.Cleanup(func() { version = "" })
	for stamp, want := range map[string]bool{
		"":                        false,
		"v1.2.3":                  true,
		"v10.20.30":               true,
		"1.2.3":                   false,
		"v1.2":                    false,
		"v1.2.3-rc.1":             false,
		"v0.1.0-SNAPSHOT-1218b88": false,
		"dev-1218b88-dirty":       false,
		"v1.2.3 ":                 false,
	} {
		version = stamp
		if got := Released(); got != want {
			t.Errorf("stamp %q: Released() = %v, want %v", stamp, got, want)
		}
	}
}
