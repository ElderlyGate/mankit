package install

import (
	"encoding/json"
	"os"
)

// The exception lets install write, not encode.
func put() {
	_ = os.WriteFile("bin", nil, 0o755)
	_, _ = json.Marshal(1) // want `json.Marshal encodes outside the store and helpout`
}
