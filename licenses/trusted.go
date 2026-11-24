package licenses

import (
	"bytes"
	"crypto/ed25519"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tugbayatilla/mankit/v4/build"
)

// trusted is the one home of the public keys every app trusts, prod and dev (ADR-L6); a key change is a mankit release.
//
//go:embed trusted.json
var trusted []byte

// trustedKey is one key of trusted.json: its id, its Ed25519 public key in base64, and the build that takes it.
type trustedKey struct {
	ID        string `json:"id"`
	PublicKey []byte `json:"publicKey"`
	Build     string `json:"build"`
}

// Trusted is the Trust every app hands its Checker: the org's keys from trusted.json, and whether this build is a release (ADR-L5).
func Trusted() (Trust, error) { return readTrust("trusted.json", trusted, build.Released()) }

// readTrust reads a list of trusted keys, or says what in the file named name is wrong: no JSON list of keys, a key without an id or build, an id listed twice, one id as both prod and dev, or a key that is not an Ed25519 public key.
func readTrust(name string, raw []byte, release bool) (Trust, error) {
	var keys []trustedKey
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&keys); err != nil {
		return Trust{}, fmt.Errorf("licenses: %s is not a list of keys: %w", name, err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return Trust{}, fmt.Errorf("licenses: %s holds more than one list of keys", name)
	}
	if keys == nil {
		return Trust{}, fmt.Errorf("licenses: %s is not a list of keys", name)
	}
	t := Trust{Release: release, Prod: map[string]ed25519.PublicKey{}, Dev: map[string]ed25519.PublicKey{}}
	for _, k := range keys {
		var builds map[string]ed25519.PublicKey
		switch k.Build {
		case "prod":
			builds = t.Prod
		case "dev":
			builds = t.Dev
		default:
			return Trust{}, fmt.Errorf("licenses: %s: the key %q has the build %q, not prod or dev", name, k.ID, k.Build)
		}
		if k.ID == "" {
			return Trust{}, fmt.Errorf("licenses: %s lists a key without an id", name)
		}
		if _, twice := builds[k.ID]; twice {
			return Trust{}, fmt.Errorf("licenses: %s lists the key %s twice", name, k.ID)
		}
		builds[k.ID] = k.PublicKey
	}
	if err := t.check(); err != nil {
		return Trust{}, fmt.Errorf("%s: %w", name, err)
	}
	return t, nil
}
