package licenses

import (
	"bytes"
	"crypto/ed25519"
	_ "embed"
	"encoding/json"
	"errors"
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

// ErrTrustedKeys is the code of every refusal of a trusted keys file; the text names the file and why.
var ErrTrustedKeys = errors.New("licenses: a trusted keys file is refused")

// keysRefused is a refusal of the trusted keys file it names, coded ErrTrustedKeys.
type keysRefused struct{ text string }

func (r keysRefused) Error() string { return r.text }
func (keysRefused) Unwrap() error   { return ErrTrustedKeys }

// refuse is the one form of every refusal: "licenses: <file>: <why>".
func refuse(name, format string, a ...any) error {
	return keysRefused{"licenses: " + name + ": " + fmt.Sprintf(format, a...)}
}

// Trusted is the Trust every app hands its Checker: the org's keys from trusted.json, and whether this build is a release (ADR-L5).
func Trusted() (Trust, error) { return readTrust("trusted.json", trusted, build.Released()) }

// readTrust reads a list of trusted keys, or refuses it with ErrTrustedKeys: no single JSON list of keys, a key without an id or build, an id listed twice, one id as both prod and dev, or a key that is not an Ed25519 public key.
func readTrust(name string, raw []byte, release bool) (Trust, error) {
	var keys []trustedKey
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&keys); err != nil {
		return Trust{}, refuse(name, "it is not a list of keys: %v", err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return Trust{}, refuse(name, "it holds more than one list of keys")
	}
	if keys == nil {
		return Trust{}, refuse(name, "it is not a list of keys")
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
			return Trust{}, refuse(name, "the key %q has the build %q, not prod or dev", k.ID, k.Build)
		}
		if k.ID == "" {
			return Trust{}, refuse(name, "it lists a key without an id")
		}
		if _, twice := builds[k.ID]; twice {
			return Trust{}, refuse(name, "it lists the key %s twice", k.ID)
		}
		builds[k.ID] = k.PublicKey
	}
	if err := t.check(); err != nil {
		return Trust{}, refuse(name, "%v", err)
	}
	return t, nil
}
