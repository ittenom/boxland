//go:build preview

package tli

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func itemByTitle(title string) item {
	for _, it := range defaultItems() {
		if it.title == title {
			return it
		}
	}
	panic("itemByTitle: no item " + title)
}

// TestPreviewMenu dumps a stripped-ANSI render of the menu (with the
// cursor on Design) for eyeballing.
//
//	go test -tags preview -run TestPreviewMenu -v ./internal/tli/...
func TestPreviewMenu(t *testing.T) {
	m := newModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 50})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	plain := ansiRE.ReplaceAllString(m.View(), "")
	fmt.Println(plain)
}

// TestPreviewMenuInstallComplete synthesises the daily-driver
// layout: Design at position 0 with the featured highlight; Check
// Installation at position 1 (no longer featured, available for
// re-running after a `git pull`).
//
//	go test -tags preview -run TestPreviewMenuInstallComplete -v ./internal/tli/...
func TestPreviewMenuInstallComplete(t *testing.T) {
	m := newModel()
	m.firstRunMissing = nil // suppress first-run card
	// Force the install-complete layout regardless of the test
	// machine's actual cwd / PATH.
	items := itemsForState(true, nil)
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	m.list.SetItems(li)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 50})
	m = updated.(model)
	plain := ansiRE.ReplaceAllString(m.View(), "")
	fmt.Println(plain)
}

// TestPreviewMenuFreshClone synthesises the fresh-clone layout:
// Check Installation at position 0, featured; Design dimmed below.
//
//	go test -tags preview -run TestPreviewMenuFreshClone -v ./internal/tli/...
func TestPreviewMenuFreshClone(t *testing.T) {
	m := newModel()
	m.firstRunMissing = nil // skip the card so we can see the menu itself
	items := itemsForState(false, nil)
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	m.list.SetItems(li)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 50})
	m = updated.(model)
	plain := ansiRE.ReplaceAllString(m.View(), "")
	fmt.Println(plain)
}

// TestPreviewSplit synthesizes a live Design job + a couple of tail
// lines (including the listening marker) so the split layout, pinned
// services strip, and stopwatch all render without spawning a real
// subprocess.
//
//	go test -tags preview -run TestPreviewSplit -v ./internal/tli/...
func TestPreviewSplit(t *testing.T) {
	t.Setenv("BOXLAND_HTTP_ADDR", ":8080")

	m := newModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 50})
	m = updated.(model)

	design := itemByTitle("Design")
	live := &job{id: design.title, it: design, started: time.Now()}
	m.jobs[design.title] = live
	m.currentIndefinite = live
	m.focus = focusLogs
	// Re-apply size so the menu pane shrinks to share width with
	// the logs pane (the real startSelected path does this for us
	// in production; the preview test sets state directly).
	m.applySize(m.width, m.height)

	for _, line := range []string{
		"  postgres   ✓ Container boxland-postgres   Started",
		"  redis      ✓ Container boxland-redis      Started",
		"  mailpit    ✓ Container boxland-mailpit    Started",
		"  minio      ✓ Container boxland-minio      Started",
		"  migrate    applying 003_publishing.sql",
		"  migrate    applying 004_hud.sql",
		"  npm        added 312 packages in 18s",
		"  build      compiled web/ in 4.7s",
		"time=2026-04-26T12:00:00Z level=INFO msg=\"http listening\" addr=:8080",
	} {
		m.appendOutput(runOutputMsg{jobID: design.title, lines: []string{line}})
	}

	plain := ansiRE.ReplaceAllString(m.View(), "")
	fmt.Println(plain)
}

// TestPreviewFailureCard shows the failure card surfacing the
// captured tail of a failed interactive job (the bug: bubbletea's
// tea.ExecProcess wipes the terminal on resume so the user only
// briefly sees the real error).
//
//	go test -tags preview -run TestPreviewFailureCard -v ./internal/tli/...
func TestPreviewFailureCard(t *testing.T) {
	m := newModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 50})
	m = updated.(model)

	m.failedJob = &failedJobView{
		title: "Install", elapsed: 4500 * time.Millisecond,
		err: errStub("exit status 1"),
		tail: []string{
			"✓ Docker Desktop  /usr/local/bin/docker",
			"✓ Go              /usr/local/bin/go",
			"✗ Node.js         missing",
			"  Trying: brew install node",
			"  Installer failed: brew: command not found",
			"  Could not install automatically. Install from https://nodejs.org/",
			"✗ npm             missing",
			"  No supported package manager found.",
			"install incomplete: Node.js, npm could not be installed automatically.",
		},
	}

	plain := ansiRE.ReplaceAllString(m.View(), "")
	fmt.Println(plain)
}

// TestPreviewIdleAfterRun shows the menu reverting to single-pane after
// a job finishes, with the toast in the list status bar.
func TestPreviewIdleAfterRun(t *testing.T) {
	m := newModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 50})
	m = updated.(model)

	test := itemByTitle("Test")
	m.jobs[test.title] = &job{id: test.title, it: test, started: time.Now().Add(-47 * time.Second)}
	updated, _ = m.Update(runDoneMsg{jobID: test.title, elapsed: 47*time.Second + 300*time.Millisecond})
	m = updated.(model)

	plain := ansiRE.ReplaceAllString(m.View(), "")
	if strings.Contains(plain, "Logs") {
		t.Logf("(unexpected) Logs pane still rendering after run done:\n%s", plain)
	}
	fmt.Println(plain)
}
