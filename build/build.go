// Package build says whether the running binary is a release or a dev build (ADR-L5), and the product line it is sold as (V-10): a goreleaser release from a tag is a release, every other build is dev.
package build

import (
	"regexp"
	"strconv"
)

// version is the tag a release is built from, stamped by goreleaser and by nothing else:
// -ldflags "-X github.com/tugbayatilla/mankit/v4/build.version=v1.2.3". It is unexported, so no code can change it after the build.
var version string

// line is the product line the build is sold as (V-10), such as 1 for Designer 1, stamped beside version and by nothing else:
// -ldflags "-X github.com/tugbayatilla/mankit/v4/build.line=1". It is unexported, so no code can change it after the build.
var line string

// tag is a release tag vX.Y.Z (V-4).
var tag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// Released reports whether this build is a release: it was stamped with a release tag. No flag, setting or environment variable switches it.
func Released() bool { return tag.MatchString(version) }

// Line is the product line this build was stamped with (V-10), such as 1 for Designer 1; 0 for a build stamped with none, or with anything but a whole number above 0.
func Line() int {
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || strconv.Itoa(n) != line {
		return 0
	}
	return n
}
