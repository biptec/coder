package buildinfo

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

var (
	buildInfo      *debug.BuildInfo
	buildInfoValid bool
	readBuildInfo  sync.Once

	externalURL     string
	readExternalURL sync.Once

	version     string
	readVersion sync.Once

	// Updated by buildinfo_slim.go on start.
	slim bool

	// Updated by buildinfo_site.go on start.
	site bool

	// Injected with ldflags at build, see scripts/build_go.sh
	tag  string
	agpl string // either "true" or "false", ldflags does not support bools
)

var biptecCustomReleaseVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

const (
	// noVersion is the reported version when the version cannot be determined.
	// Usually because `go build` is run instead of `make build`.
	noVersion = "v0.0.0"

	// develPreRelease is the pre-release tag for developer versions of the
	// application. This includes CI builds. The pre-release tag should be appended
	// to the version with a "-".
	// Example: v0.0.0-devel
	develPreRelease = "devel"
)

// Version returns the user-facing version of the build. Official Coder builds
// use semantic versions. Biptec custom releases use vMAJOR.MINOR.PATCH.REVISION
// so operators can identify the exact custom release. Use Semver or
// CompareVersions before applying golang.org/x/mod/semver operations.
func Version() string {
	readVersion.Do(func() {
		revision, valid := Revision()
		if valid {
			revision = "+" + revision[:7]
		}
		if tag == "" {
			// This occurs when the tag hasn't been injected,
			// like when using "go run".
			// <version>-<pre-release>+<revision>
			version = fmt.Sprintf("%s-%s%s", noVersion, develPreRelease, revision)
			return
		}
		version = "v" + tag
		// The tag must be prefixed with "v" otherwise the
		// semver library will return an empty string.
		if semver.Build(version) == "" {
			version += revision
		}
	})
	return version
}

// Semver converts a user-facing Coder version into a semantic version suitable
// for validation and semantic comparisons. Standard semantic versions are
// returned unchanged. Biptec custom releases preserve their fourth numeric
// component as build metadata, for example:
//
//	v2.35.3.2+ed8ad7d -> v2.35.3+biptec.2.ed8ad7d
//
// An empty string means the version is not recognized.
func Semver(v string) string {
	if semver.IsValid(v) {
		return v
	}

	parts := biptecCustomReleaseVersion.FindStringSubmatch(v)
	if parts == nil {
		return ""
	}

	metadata := "biptec." + parts[4]
	if parts[5] != "" {
		metadata += "." + parts[5]
	}
	return fmt.Sprintf("v%s.%s.%s+%s", parts[1], parts[2], parts[3], metadata)
}

// IsValidVersion reports whether v is either a standard semantic version or a
// Biptec custom release version.
func IsValidVersion(v string) bool {
	return Semver(v) != ""
}

// CompareVersions compares standard semantic versions and Biptec custom release
// versions. Custom revisions are ordered numerically after the upstream patch
// they extend, so v2.35.3.3 is newer than v2.35.3.2 and v2.35.3, but older
// than v2.35.4. Invalid versions follow semver.Compare's invalid-version
// ordering semantics.
func CompareVersions(v1, v2 string) int {
	s1 := Semver(v1)
	s2 := Semver(v2)
	if s1 == "" || s2 == "" {
		return semver.Compare(s1, s2)
	}

	p1 := biptecCustomReleaseVersion.FindStringSubmatch(v1)
	p2 := biptecCustomReleaseVersion.FindStringSubmatch(v2)

	if cmp := semver.Compare(semver.Canonical(s1), semver.Canonical(s2)); cmp != 0 {
		return cmp
	}

	switch {
	case p1 != nil && p2 != nil:
		// Custom revisions have no leading zeroes, so decimal string length
		// followed by lexical order gives exact numeric ordering without an
		// artificial integer-size limit.
		switch {
		case len(p1[4]) < len(p2[4]):
			return -1
		case len(p1[4]) > len(p2[4]):
			return 1
		case p1[4] < p2[4]:
			return -1
		case p1[4] > p2[4]:
			return 1
		default:
			return 0
		}
	case p1 != nil:
		return 1
	case p2 != nil:
		return -1
	default:
		return semver.Compare(s1, s2)
	}
}

// VersionsMatch compares the two versions. It assumes the versions match if
// the major and the minor versions are equivalent. Patch versions are
// disregarded. If it detects that either version is a developer build it
// returns true.
func VersionsMatch(v1, v2 string) bool {
	// If no version is attached, then it is a dev build outside of CI. The version
	// will be disregarded... hopefully they know what they are doing.
	if strings.Contains(v1, noVersion) || strings.Contains(v2, noVersion) {
		return true
	}

	s1 := Semver(v1)
	s2 := Semver(v2)
	if s1 == "" || s2 == "" {
		return false
	}
	return semver.MajorMinor(s1) == semver.MajorMinor(s2)
}

func IsDevVersion(v string) bool {
	return strings.Contains(v, "-"+develPreRelease)
}

// IsRCVersion returns true if the version has a release candidate
// pre-release tag, e.g. "v2.31.0-rc.0".
func IsRCVersion(v string) bool {
	return strings.Contains(v, "-rc.")
}

// IsDev returns true if this is a development build.
// CI builds are also considered development builds.
func IsDev() bool {
	return IsDevVersion(Version())
}

// IsSlim returns true if this is a slim build.
func IsSlim() bool {
	return slim
}

// HasSite returns true if the frontend is embedded in the build.
func HasSite() bool {
	return site
}

// IsAGPL returns true if this is an AGPL build.
func IsAGPL() bool {
	return strings.Contains(agpl, "t")
}

func IsBoringCrypto() bool {
	return boringcrypto
}

// ExternalURL returns a URL referencing the current Coder version.
// For production builds, this will link directly to a release.
// For development builds, this will link to a commit.
func ExternalURL() string {
	readExternalURL.Do(func() {
		repo := "https://github.com/coder/coder"
		revision, valid := Revision()
		if !valid {
			externalURL = repo
			return
		}
		externalURL = fmt.Sprintf("%s/commit/%s", repo, revision)
	})
	return externalURL
}

// Time returns when the Git revision was published.
func Time() (time.Time, bool) {
	value, valid := find("vcs.time")
	if !valid {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic("couldn't parse time: " + err.Error())
	}
	return parsed, true
}

// Revision returns the full Git hash of the build.
func Revision() (string, bool) {
	return find("vcs.revision")
}

// find panics if a setting with the specific key was not
// found in the build info.
func find(key string) (string, bool) {
	readBuildInfo.Do(func() {
		buildInfo, buildInfoValid = debug.ReadBuildInfo()
	})
	if !buildInfoValid {
		panic("couldn't read build info")
	}
	for _, setting := range buildInfo.Settings {
		if setting.Key != key {
			continue
		}
		return setting.Value, true
	}
	return "", false
}
