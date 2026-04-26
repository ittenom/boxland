//go:build preview

package tli

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// TestPreviewMenu dumps a stripped-ANSI render of the menu (with the
// cursor on Design) for eyeballing.
//
//	go test -tags preview -run TestPreviewMenu -v ./internal/tli/...
func TestPreviewMenu(t *testing.T) {
	m := newModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 50})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	plain := ansiRE.ReplaceAllString(m.View(), "")
	fmt.Println(plain)
}

// TestPreviewRun fakes a phaseRun state with a tail buffer and a partially-
// elapsed stopwatch so the runner view layout is visible without spinning
// up a real subprocess.
//
//	go test -tags preview -run TestPreviewRun -v ./internal/tli/...
func TestPreviewRun(t *testing.T) {
	m := newModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 50})
	m = updated.(model)

	// Synthesize phaseRun.
	m.phase = phaseRun
	m.running = item{
		title: "Design", badge: "quick start", featured: true,
		cmd:        []string{"boxland", "design"},
		indefinite: true,
	}
	m.tail.SetContent(strings.Join([]string{
		"  postgres   ✓ Container boxland-postgres   Started",
		"  redis      ✓ Container boxland-redis      Started",
		"  mailpit    ✓ Container boxland-mailpit    Started",
		"  minio      ✓ Container boxland-minio      Started",
		"  migrate    applying 003_publishing.sql",
		"  migrate    applying 004_hud.sql",
		"  npm        added 312 packages in 18s",
		"  build      compiled web/ in 4.7s",
		"  serve      http listening on :8080",
	}, "\n"))
	// Spoof a stopwatch elapsed value via successive ticks would be tedious;
	// just print the layout assuming 0:00.0 — the structure is the point.

	plain := ansiRE.ReplaceAllString(m.View(), "")
	fmt.Println(plain)
}

// TestPreviewRunDone shows the post-completion summary state.
func TestPreviewRunDone(t *testing.T) {
	m := newModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 50})
	m = updated.(model)

	m.phase = phaseRun
	m.running = item{title: "Test", badge: "quality", cmd: []string{"boxland", "test"}}
	m.runDone = true
	m.runElapsed = 47*time.Second + 300*time.Millisecond
	m.tail.SetContent("  ok   boxland/server/internal/maps     0.318s\n  ok   boxland/server/internal/assets    0.224s\n  ok   boxland/server/internal/entities  0.451s")

	plain := ansiRE.ReplaceAllString(m.View(), "")
	fmt.Println(plain)
}
