package boxlandcmd

import (
	"os"
	"path/filepath"
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

// TestRefuseIfRootOnMac — when the user has run `boxland install`
// under sudo on macOS, refuseIfRootOnMac must abort with a message
// that points them at the right next step (drop the sudo). Homebrew
// hard-aborts on root invocations, so letting this through would
// just produce a stream of confusing "Don't run this as root!"
// errors.
//
// We swap currentUID rather than actually running as root so the
// test is portable to dev laptops and CI without privileged setup.
func TestRefuseIfRootOnMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("guard only fires on macOS; on other OSes it's a no-op")
	}
	orig := currentUID
	t.Cleanup(func() { currentUID = orig })

	currentUID = func() int { return 0 }
	err := refuseIfRootOnMac()
	if err == nil {
		t.Fatal("expected refuseIfRootOnMac to error when EUID is 0 on darwin")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error should mention sudo so the user knows what to drop; got %v", err)
	}

	currentUID = func() int { return 501 }
	if err := refuseIfRootOnMac(); err != nil {
		t.Fatalf("non-root user should pass through; got %v", err)
	}
}

// TestRefuseIfRootOnMacIsNoOpElsewhere — Linux package managers
// (apt/dnf) actually want sudo, so the guard must not fire on
// non-darwin even when EUID is 0.
func TestRefuseIfRootOnMacIsNoOpElsewhere(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("test asserts non-darwin behaviour")
	}
	orig := currentUID
	t.Cleanup(func() { currentUID = orig })
	currentUID = func() int { return 0 }
	if err := refuseIfRootOnMac(); err != nil {
		t.Fatalf("refuseIfRootOnMac on %s should be a no-op even as root; got %v", runtime.GOOS, err)
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

// TestInstallCommandWingetUsesUserScope pins `--scope user` on
// Windows winget installs. Without it, packages whose installer can
// reach for a machine-scope path (like Chocolatey was doing for
// flatc) hit a UAC prompt — which boxland install, deliberately
// running unelevated, has no way to satisfy. Regression guard for
// the bug where `boxland install` couldn't install flatc on
// unelevated Windows.
func TestInstallCommandWingetUsesUserScope(t *testing.T) {
	got := installCommand("winget", "Foo.Bar")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--scope user") {
		t.Errorf("winget install command must pass --scope user so unelevated installs land in the per-user prefix; got %q", joined)
	}
	if !strings.Contains(joined, "--exact") {
		t.Errorf("winget install command must keep --exact so we don't accept a fuzzy id match; got %q", joined)
	}
}

// TestFlatcWingetIdIsLowercase pins the corrected `Google.flatbuffers`
// id. The published winget package id is lowercase; with our --exact
// flag, the previous `Google.FlatBuffers` value silently failed to
// match, falling through to chocolatey which needed admin we don't
// have. This test catches a future case-typo regression at test
// time, not on a customer's Windows box.
func TestFlatcWingetIdIsLowercase(t *testing.T) {
	for _, r := range installRequirements() {
		if r.Cmd != "flatc" {
			continue
		}
		got := r.Packages["winget"]
		if got != "Google.flatbuffers" {
			t.Errorf("flatc winget id must be %q (lowercase, matches the published winget-pkgs entry); got %q", "Google.flatbuffers", got)
		}
		return
	}
	t.Fatal("no flatc entry in installRequirements()")
}

// TestSqlcWindowsHasGoInstallFallback — sqlc has no current winget
// package (sqlc-dev.sqlc was withdrawn) and chocolatey would need
// admin we don't have. The only path that works on an unelevated
// Windows shell is `go install`, which is fine because Go is a
// required earlier-in-list dependency. Pin the fallback so a future
// edit doesn't accidentally drop it and re-break Windows installs.
func TestSqlcWindowsHasGoInstallFallback(t *testing.T) {
	for _, r := range installRequirements() {
		if r.Cmd != "sqlc" {
			continue
		}
		got := r.Packages["goinstall"]
		if !strings.HasPrefix(got, "github.com/sqlc-dev/sqlc/cmd/sqlc@") {
			t.Errorf("sqlc must have a goinstall fallback rooted at github.com/sqlc-dev/sqlc/cmd/sqlc@<ver>; got %q", got)
		}
		return
	}
	t.Fatal("no sqlc entry in installRequirements()")
}

// TestInstallCommandGoInstall — the goinstall pseudo-PM must shell
// out to `go install <module>`. Cheap shape check so a future
// installCommand refactor doesn't silently swap the verb.
func TestInstallCommandGoInstall(t *testing.T) {
	got := installCommand("goinstall", "github.com/sqlc-dev/sqlc/cmd/sqlc@latest")
	want := []string{"go", "install", "github.com/sqlc-dev/sqlc/cmd/sqlc@latest"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("goinstall command shape changed; got %v, want %v", got, want)
	}
}

// TestSqlcLinuxAttemptsGoInstallFirst pins the Linux sqlc install path:
// prefer `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest` before
// trying distro package managers.
func TestSqlcLinuxAttemptsGoInstallFirst(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("asserts generic Linux install ordering")
	}
	bin := t.TempDir()
	for _, name := range []string{"go", "zypper"} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("PATH", bin)

	var sqlc installRequirement
	for _, r := range installRequirements() {
		if r.Cmd == "sqlc" {
			sqlc = r
			break
		}
	}
	if sqlc.Cmd == "" {
		t.Fatal("no sqlc entry in installRequirements()")
	}

	attempts := installAttempts(sqlc)
	if len(attempts) == 0 {
		t.Fatal("expected at least one sqlc install attempt")
	}
	want := []string{"go", "install", "github.com/sqlc-dev/sqlc/cmd/sqlc@latest"}
	if strings.Join(attempts[0], " ") != strings.Join(want, " ") {
		t.Fatalf("first Linux sqlc install attempt = %v, want %v", attempts[0], want)
	}
}

// TestInstallCommandZypper — OpenSUSE users should get a native
// zypper install attempt from the TUI's Check Installation path
// (`boxland install`) before we fall back to manual links.
func TestInstallCommandZypper(t *testing.T) {
	got := installCommand("zypper", "nodejs npm")
	want := []string{"sudo", "zypper", "install", "-y", "nodejs", "npm"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("zypper command shape changed; got %v, want %v", got, want)
	}
}

// TestPackageManagersForPlatformIncludesGoInstall — every platform
// must list `goinstall` so requirements that opt into it (sqlc
// today) actually get the fallback considered. Without this the
// installAttempts loop would skip past go install entirely.
func TestPackageManagersForPlatformIncludesGoInstall(t *testing.T) {
	pms := packageManagersForPlatform()
	for _, pm := range pms {
		if pm == "goinstall" {
			return
		}
	}
	t.Errorf("packageManagersForPlatform() must include \"goinstall\" on %s; got %v", runtime.GOOS, pms)
}

// TestLinuxPackageManagersIncludeZypper pins OpenSUSE support in the
// installer order. On non-Linux platforms we still assert zypper is
// not accidentally offered where it does not belong.
func TestLinuxPackageManagersIncludeZypper(t *testing.T) {
	pms := packageManagersForPlatform()
	hasZypper := false
	for _, pm := range pms {
		if pm == "zypper" {
			hasZypper = true
			break
		}
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if hasZypper {
			t.Errorf("zypper should only be a generic Linux package manager; got %v on %s", pms, runtime.GOOS)
		}
		return
	}
	if !hasZypper {
		t.Errorf("packageManagersForPlatform() must include zypper on %s; got %v", runtime.GOOS, pms)
	}
}

// TestInstallRequirementsHaveZypperPackages keeps the OpenSUSE package
// map from regressing for requirements that cannot rely on go install.
func TestInstallRequirementsHaveZypperPackages(t *testing.T) {
	for _, r := range installRequirements() {
		if r.Cmd == "sqlc" {
			continue // covered by TestSqlcWindowsHasGoInstallFallback.
		}
		if got := r.Packages["zypper"]; got == "" {
			t.Errorf("%s (%s) is missing a zypper package mapping", r.Name, r.Cmd)
		}
	}
}

// TestFreshInstallPathDirsIncludesWingetLinksOnWindows — after a
// winget portable install (flatc), the binary lands in
// %LOCALAPPDATA%\Microsoft\WinGet\Links. That dir is not on the
// running process's PATH on a fresh first-time winget install, so
// refreshInstallPath must explicitly add it; otherwise the
// post-install LookPath retry fails and we report flatc as
// unresolved even though winget just succeeded.
func TestFreshInstallPathDirsIncludesWingetLinksOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("winget Links dir is Windows-only")
	}
	t.Setenv("LOCALAPPDATA", `C:\Users\test\AppData\Local`)
	dirs := freshInstallPathDirs()
	want := `C:\Users\test\AppData\Local\Microsoft\WinGet\Links`
	for _, d := range dirs {
		if d == want {
			return
		}
	}
	t.Errorf("freshInstallPathDirs() must include %q so winget portable shims become discoverable mid-run; got %v", want, dirs)
}

// TestGoBinDirHonorsGOBIN — `go install` writes into $GOBIN when it
// is set, so refreshInstallPath must look there first. If we ignored
// GOBIN, a user with a custom layout would see boxland install
// "succeed" running `go install` and then fail to find the binary.
func TestGoBinDirHonorsGOBIN(t *testing.T) {
	t.Setenv("GOBIN", filepath.Join(t.TempDir(), "custom-gobin"))
	t.Setenv("GOPATH", "")
	got := goBinDir()
	if got != os.Getenv("GOBIN") {
		t.Errorf("goBinDir should return $GOBIN when set; got %q, want %q", got, os.Getenv("GOBIN"))
	}
}

// TestGoBinDirFallsBackToGOPATHBin — second-precedence fallback,
// matching `go help gopath`: the bin dir lives at $GOPATH/bin (and
// the first entry of GOPATH wins on multi-entry layouts).
func TestGoBinDirFallsBackToGOPATHBin(t *testing.T) {
	gopath := filepath.Join(t.TempDir(), "gp")
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", gopath)
	want := filepath.Join(gopath, "bin")
	if got := goBinDir(); got != want {
		t.Errorf("goBinDir should return $GOPATH/bin when GOBIN is empty; got %q, want %q", got, want)
	}
}

// TestRequiredCmdsMatchesInstallRequirements is a tripwire: the TUI's
// "is the install complete?" decision (in internal/tui) reads from
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
			"Keep these in lockstep so the TUI can correctly detect when install is complete.",
			want, got)
	}
}
