package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUpdateFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want updateFlags
	}{
		{"empty", nil, updateFlags{}},
		{"check", []string{"--check"}, updateFlags{check: true}},
		{"force", []string{"--force"}, updateFlags{force: true}},
		{"no-backup", []string{"--no-backup"}, updateFlags{noBackup: true}},
		{"resume", []string{"--resume"}, updateFlags{resume: true}},
		{"combined", []string{"--force", "--no-backup"}, updateFlags{force: true, noBackup: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseUpdateFlags(c.args)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseUpdateFlags_UnknownFlagFails(t *testing.T) {
	if _, err := parseUpdateFlags([]string{"--never-defined"}); err == nil {
		t.Fatalf("unknown flag should error")
	}
}

func TestSanitizeForFilename(t *testing.T) {
	cases := map[string]string{
		"":                "unknown",
		"v0.1.0":          "v0.1.0",
		"v0.1.0+build/1":  "v0.1.0+build-1",
		"with spaces":     "with-spaces",
	}
	for in, want := range cases {
		if got := sanitizeForFilename(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

// printMigrationNotes — when MIGRATION_NOTES.md has a matching
// section, its body is printed; when it doesn't, the function is
// silent (no error, no panic).

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prev })
	done := make(chan []byte)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	fn()
	_ = w.Close()
	return string(<-done)
}

func TestPrintMigrationNotes_FoundWithVPrefix(t *testing.T) {
	dir := t.TempDir()
	mn := filepath.Join(dir, "MIGRATION_NOTES.md")
	const body = `# Notes

## v0.2.0

Heads up: this release renamed the doodads table.

## v0.1.0

First release.
`
	if err := os.WriteFile(mn, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	out := captureStdout(t, func() { printMigrationNotes("0.2.0") })
	if !strings.Contains(out, "renamed the doodads table") {
		t.Errorf("expected v0.2.0 body in output, got %q", out)
	}
	if strings.Contains(out, "First release.") {
		t.Errorf("output bled into next section: %q", out)
	}
}

func TestPrintMigrationNotes_MissingIsSilent(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	out := captureStdout(t, func() { printMigrationNotes("9.9.9") })
	if out != "" {
		t.Errorf("missing notes should produce no output, got %q", out)
	}
}

// gitWorkingTreeDirty + gitCurrentBranch — exercise against a
// throw-away git repo so we don't depend on the surrounding repo's
// state.

func TestGitGuards(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q", "-b", "main")
	mustGit("config", "user.email", "test@example.com")
	mustGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("add", "x")
	mustGit("commit", "-q", "-m", "init")

	// Clean tree, on main.
	dirty, err := gitWorkingTreeDirty(dir)
	if err != nil {
		t.Fatalf("dirty err: %v", err)
	}
	if dirty {
		t.Errorf("freshly-committed tree shows dirty")
	}
	branch, err := gitCurrentBranch(dir)
	if err != nil {
		t.Fatalf("branch err: %v", err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want main", branch)
	}

	// Touch a file to dirty the tree.
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("hi2"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = gitWorkingTreeDirty(dir)
	if err != nil {
		t.Fatalf("dirty err 2: %v", err)
	}
	if !dirty {
		t.Errorf("modified file should mark tree dirty")
	}
}
