// Package build says whether the running binary is a release or a dev build (ADR-L5): a goreleaser release from a tag is a release, every other build is dev.
package build

import "regexp"

// version is the tag a release is built from, stamped by goreleaser and by nothing else:
// -ldflags "-X go.naywee.com/mankit/v5/build.version=v1.2.3". It is unexported, so no code can change it after the build.
var version string

// tag is a release tag vX.Y.Z (V-4).
var tag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// Released reports whether this build is a release: it was stamped with a release tag. No flag, setting or environment variable switches it.
func Released() bool { return tag.MatchString(version) }
