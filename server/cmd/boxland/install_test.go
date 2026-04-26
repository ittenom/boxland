package main

import (
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"

	"boxland/server/internal/setup"
)

// TestEnsureRequirementMissingNoPM verifies that a requirement which
// is not on PATH and has no package-manager entry for the current
// platform returns a non-nil error. Regression guard for the bug
// where `Install` silently swept missing dependencies under the rug
// and let a later `npm install` blow up with a confusing "executable
// file not found" before the user could see what was actually
// missing.
func TestEnsureRequirementMissingNoPM(t *testing.T) {
	r := installRequirement{
		// Use a name we know isn't on any PATH so LookPath fails.
		Name:        "bogus-test-tool",
		Cmd:         "bogus-test-tool-xyz-no-one-has-this",
		VersionArgs: []string{"--version"},
		URL:         "https://example.invalid/",
		// Empty Packages map -> no PM candidate matches the platform.
		Packages: map[string]string{},
	}
	err := ensureRequirement(r)
	if err == nil {
		t.Fatal("expected ensureRequirement to return an error for a missing tool with no PM, got nil")
	}
	if !strings.Contains(err.Error(), "bogus-test-tool") {
		t.Errorf("error should mention the tool name; got %v", err)
	}
}

// TestEnsureBrewOnMacIsNoOpElsewhere — outside macOS, brew bootstrap
// must not touch PATH or invoke anything. Cheap regression guard
// against accidentally breaking Linux/Windows Install runs.
func TestEnsureBrewOnMacIsNoOpElsewhere(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("test asserts non-darwin behaviour")
	}
	pathBefore := os.Getenv("PATH")
	if err := ensureBrewOnMac(); err != nil {
		t.Fatalf("ensureBrewOnMac on %s should be a no-op, got error: %v", runtime.GOOS, err)
	}
	if pathBefore != os.Getenv("PATH") {
		t.Error("ensureBrewOnMac mutated PATH on a non-darwin platform")
	}
}

// TestPrependPathIdempotent — calling prependPath twice with the
// same dir should leave PATH with one entry (not two), so repeated
// Install runs don't bloat the environment.
func TestPrependPathIdempotent(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	prependPath("/opt/homebrew/bin")
	prependPath("/opt/homebrew/bin")
	got := os.Getenv("PATH")
	if strings.Count(got, "/opt/homebrew/bin") != 1 {
		t.Errorf("prependPath should be idempotent; got PATH=%q", got)
	}
	if !strings.HasPrefix(got, "/opt/homebrew/bin") {
		t.Errorf("prependPath should put dir at front; got PATH=%q", got)
	}
}

// TestPrependPathHandlesEmpty — when PATH is empty (rare, but
// possible in stripped environments), prependPath should set it
// without leaving a leading separator.
func TestPrependPathHandlesEmpty(t *testing.T) {
	t.Setenv("PATH", "")
	prependPath("/opt/homebrew/bin")
	if got := os.Getenv("PATH"); got != "/opt/homebrew/bin" {
		t.Errorf("empty-PATH prepend; got %q", got)
	}
}

// TestRequiredCmdsMatchesInstallRequirements is a tripwire: the TLI's
// "is the install complete?" decision (in internal/tli) reads from
// setup.RequiredCmds(); runInstall here actually probes for those
// tools via installRequirements(). Both lists must enumerate the
// same .Cmd values, in the same set, or the menu will lie about the
// install being complete (or never let go of the first-run card).
func TestRequiredCmdsMatchesInstallRequirements(t *testing.T) {
	want := setup.RequiredCmds()
	var got []string
	for _, r := range installRequirements() {
		got = append(got, r.Cmd)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, ",") != strings.Join(got, ",") {
		t.Fatalf("setup.RequiredCmds() = %v; installRequirements() Cmd values = %v.\n"+
			"Keep these in lockstep so the TLI can correctly detect when install is complete.",
			want, got)
	}
}
