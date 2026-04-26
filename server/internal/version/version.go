// Package version is the single source of truth for the running
// Boxland version.
//
// The canonical SemVer string lives in this package's VERSION file.
// We embed it (rather than reading from disk at runtime) so:
//
//   - `go run ./server/cmd/boxland` from a fresh checkout works
//     without any preprocessor step,
//   - a built binary carries the version even when run far from the
//     repo root, and
//   - a release pipeline can override it via
//     `-ldflags "-X boxland/server/internal/version.override=v0.2.0"`
//     for nightly / commit-suffixed builds without touching the file.
//
// SemVer is mandatory: every release tag should be `vMAJOR.MINOR.PATCH`
// and the embedded VERSION file should match (without the leading `v`,
// to keep it easy to read and diff). The updater package uses Compare
// to decide whether the upstream release is newer than what we're
// running.
package version

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed VERSION
var embedded string

// override is set by the release toolchain via -ldflags. Empty in
// normal builds; when non-empty, it wins over the embedded file so a
// nightly build can carry a commit-suffixed version like
// `0.2.0-rc1+abc1234` without rewriting the source tree.
var override string

// Current returns the running Boxland version as a bare SemVer string
// like "0.1.0" (no leading `v`). It always returns a non-empty value;
// if both the embed and the override are empty (impossible in a
// normal build, but guarded anyway), it falls back to "0.0.0-dev" so
// the rest of the system still has something to render.
func Current() string {
	if v := strings.TrimSpace(override); v != "" {
		return normalize(v)
	}
	if v := strings.TrimSpace(embedded); v != "" {
		return normalize(v)
	}
	return "0.0.0-dev"
}

// IsDev reports whether the running version is a development build
// (no real release). The updater uses this to short-circuit: a dev
// build never claims an update is available, because comparing
// "0.0.0-dev" against a real tag would always trigger noise.
func IsDev() string {
	v := Current()
	if v == "0.0.0-dev" || strings.Contains(v, "-dev") {
		return v
	}
	return ""
}

// normalize strips a single leading `v` so callers can be loose about
// whether they pass "v0.1.0" or "0.1.0". Everything inside this
// package compares the bare form.
func normalize(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		return v[1:]
	}
	return v
}

// Parsed is a SemVer triple plus an optional pre-release tag (the
// piece after `-`). Build metadata (`+abc1234`) is preserved on the
// struct but ignored by Compare per SemVer §10.
type Parsed struct {
	Major, Minor, Patch int
	PreRelease          string // empty for release versions
	Build               string // empty when no `+meta`
	Raw                 string // the original input, post-normalize
}

// Parse converts a SemVer string into a Parsed. It accepts and strips
// a single leading `v`. Returns an error for anything that doesn't
// look like `MAJOR.MINOR.PATCH[-pre][+build]`.
func Parse(s string) (Parsed, error) {
	out := Parsed{Raw: normalize(s)}
	core := out.Raw
	if i := strings.IndexByte(core, '+'); i >= 0 {
		out.Build = core[i+1:]
		core = core[:i]
	}
	if i := strings.IndexByte(core, '-'); i >= 0 {
		out.PreRelease = core[i+1:]
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", s)
	}
	var err error
	if out.Major, err = strconv.Atoi(parts[0]); err != nil {
		return out, fmt.Errorf("version %q: bad major: %w", s, err)
	}
	if out.Minor, err = strconv.Atoi(parts[1]); err != nil {
		return out, fmt.Errorf("version %q: bad minor: %w", s, err)
	}
	if out.Patch, err = strconv.Atoi(parts[2]); err != nil {
		return out, fmt.Errorf("version %q: bad patch: %w", s, err)
	}
	return out, nil
}

// Compare returns -1, 0, +1 in the usual sense (a < b, a == b,
// a > b) under SemVer ordering rules. Pre-release versions sort
// before their release counterpart (1.0.0-rc1 < 1.0.0). Build
// metadata is ignored. Unparseable inputs sort *before* parseable
// ones so "garbage < 0.1.0" — that way a corrupt cache never claims
// it's newer than the real running version.
func Compare(a, b string) int {
	pa, errA := Parse(a)
	pb, errB := Parse(b)
	switch {
	case errA != nil && errB != nil:
		return strings.Compare(a, b)
	case errA != nil:
		return -1
	case errB != nil:
		return +1
	}
	if c := cmpInt(pa.Major, pb.Major); c != 0 {
		return c
	}
	if c := cmpInt(pa.Minor, pb.Minor); c != 0 {
		return c
	}
	if c := cmpInt(pa.Patch, pb.Patch); c != 0 {
		return c
	}
	// SemVer §11: a release > the same triple with any pre-release.
	switch {
	case pa.PreRelease == "" && pb.PreRelease == "":
		return 0
	case pa.PreRelease == "":
		return +1
	case pb.PreRelease == "":
		return -1
	default:
		return strings.Compare(pa.PreRelease, pb.PreRelease)
	}
}

// IsNewer is sugar for Compare(latest, current) > 0 — true when
// `latest` should prompt the user to update.
func IsNewer(current, latest string) bool {
	return Compare(latest, current) > 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return +1
	default:
		return 0
	}
}
