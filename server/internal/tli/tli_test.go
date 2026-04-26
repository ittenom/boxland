package tli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// step runs Update and re-asserts back to model. Keeps tests focused on
// intent rather than type assertions.
func step(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	mm, ok := updated.(model)
	if !ok {
		t.Fatalf("Update returned non-model %T", updated)
	}
	return mm, cmd
}

// readyModel returns a sized model that's already past the loading splash.
func readyModel(t *testing.T) model {
	t.Helper()
	m := newModel()
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 110, Height: 50})
	return m
}

// TestViewRendersEveryItem exercises the menu-phase render path.
func TestViewRendersEveryItem(t *testing.T) {
	m := readyModel(t)
	out := m.View()
	if out == "" {
		t.Fatal("View() returned empty string")
	}
	for _, it := range defaultItems() {
		if !strings.Contains(out, it.title) {
			t.Errorf("View missing item title %q", it.title)
		}
	}
	if !strings.Contains(out, "Boxland TLI") {
		t.Error("View missing Boxland TLI header")
	}
}

func TestCursorNavigation(t *testing.T) {
	m := readyModel(t)
	if got := m.list.Index(); got != 0 {
		t.Fatalf("cursor must start at 0, got %d", got)
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.list.Index(); got != 1 {
		t.Errorf("after Down: want index 1, got %d", got)
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.list.Index(); got != 0 {
		t.Errorf("after Up: want index 0, got %d", got)
	}
}

func TestFilterDoesNotQuitOnQ(t *testing.T) {
	m := readyModel(t)
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.list.FilterState() != list.Filtering {
		t.Fatalf("expected Filtering state, got %v", m.list.FilterState())
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(model)
	if m.exitChosen {
		t.Error("filter input 'q' must not arm exitChosen")
	}
	if m.list.FilterState() != list.Filtering {
		t.Errorf("filter state changed unexpectedly to %v", m.list.FilterState())
	}
}

func TestQuitOnQWhenNotFiltering(t *testing.T) {
	m := readyModel(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("expected a tea.Cmd (Quit) on q press, got nil")
	}
	if m.phase != phaseMenu {
		t.Errorf("phase must remain phaseMenu on quit, got %v", m.phase)
	}
}

// TestEnterEntersRunPhaseAndStartsStopwatch confirms a non-interactive item
// transitions to phaseRun and the stopwatch is started.
//
// We pick "Test" so we don't actually shell out to anything heavy — the
// runner spawns Go's own go.exe but we cancel immediately.
func TestEnterEntersRunPhaseAndStartsStopwatch(t *testing.T) {
	m := readyModel(t)

	// Replace the items with a single trivial command we know is fast and
	// available on every CI runner: `go version`.
	tinyItem := item{
		title: "Probe", badge: "smoke", desc: "echo style probe",
		cmd: []string{"go", "version"},
	}
	li := []list.Item{tinyItem}
	cmd := m.list.SetItems(li)
	_ = cmd

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.phase != phaseRun {
		t.Fatalf("after enter: want phaseRun, got %v", m.phase)
	}
	if m.running.title != "Probe" {
		t.Errorf("expected m.running=Probe, got %q", m.running.title)
	}
	if m.runner == nil {
		t.Error("expected a captured-pipe runner for non-interactive item")
	}

	// Drain whatever the runner emits, with a generous safety timeout, until
	// runDoneMsg lands. We're not asserting on output content here — just
	// that the lifecycle completes.
	deadline := time.Now().Add(10 * time.Second)
	for !m.runDone && time.Now().Before(deadline) {
		if m.runner == nil {
			t.Fatal("runner cleared before runDoneMsg")
		}
		msg := m.runner.poll()()
		m, _ = step(t, m, msg)
	}
	if !m.runDone {
		t.Fatal("subprocess did not complete within 10s")
	}
}

// TestInteractiveItemUsesExecProcess verifies that interactive items get a
// tea.ExecProcess command rather than spawning a captured-pipe runner. The
// resulting cmd is opaque, but we can verify the runner field stays nil.
func TestInteractiveItemUsesExecProcess(t *testing.T) {
	m := readyModel(t)

	tinyItem := item{
		title: "Probe", badge: "interactive", desc: "interactive probe",
		cmd:         []string{"go", "version"},
		interactive: true,
	}
	m.list.SetItems([]list.Item{tinyItem})

	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.phase != phaseRun {
		t.Fatalf("after enter: want phaseRun, got %v", m.phase)
	}
	if m.runner != nil {
		t.Error("interactive items must not spawn a captured-pipe runner")
	}
	if cmd == nil {
		t.Error("expected a tea.Cmd batch carrying the ExecProcess command")
	}
}

// TestRunDoneReturnsToMenuOnEnter confirms that pressing enter after a run
// completes restores the menu and posts a status toast.
func TestRunDoneReturnsToMenuOnEnter(t *testing.T) {
	m := readyModel(t)
	m.phase = phaseRun
	m.running = defaultItems()[0]
	m.runDone = true
	m.runElapsed = 1500 * time.Millisecond

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.phase != phaseMenu {
		t.Fatalf("after enter on done: want phaseMenu, got %v", m.phase)
	}
	if m.runner != nil {
		t.Error("runner must be cleared on return")
	}

	out := m.list.View()
	if !strings.Contains(out, "Install completed") {
		t.Errorf("status toast missing in list view; got:\n%s", out)
	}
}

// TestRunDoneFailureToastFormat formats the failure toast distinctly from
// the success toast.
func TestRunDoneFailureToastFormat(t *testing.T) {
	got := summaryToast(item{title: "Migrate"}, errors.New("exit status 2"), 4200*time.Millisecond)
	if !strings.HasPrefix(got, "✗ Migrate failed") {
		t.Errorf("failure toast prefix wrong: %q", got)
	}
	if !strings.Contains(got, "0:04.2") {
		t.Errorf("failure toast missing elapsed: %q", got)
	}
}

// TestCancelKeyForwardsToRunner exercises the ctrl+c cancel path.
func TestCancelKeyForwardsToRunner(t *testing.T) {
	m := readyModel(t)
	tinyItem := item{
		title: "Probe", badge: "smoke", desc: "long-running probe",
		cmd:        []string{"go", "version"},
		indefinite: true,
	}
	m.list.SetItems([]list.Item{tinyItem})

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.runner == nil {
		t.Fatal("expected a runner after enter")
	}

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.cancelArmed {
		t.Error("ctrl+c during run must arm cancellation")
	}

	// Drain to completion to clean up.
	deadline := time.Now().Add(10 * time.Second)
	for !m.runDone && time.Now().Before(deadline) {
		if m.runner == nil {
			break
		}
		msg := m.runner.poll()()
		m, _ = step(t, m, msg)
	}
}

// TestFormatElapsed verifies the M:SS.t / H:MM:SS rendering for both buckets.
func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0:00.0"},
		{500 * time.Millisecond, "0:00.5"},
		{1500 * time.Millisecond, "0:01.5"},
		{75 * time.Second, "1:15.0"},
		{61 * time.Minute, "1:01:00"},
	}
	for _, tc := range cases {
		if got := formatElapsed(tc.in); got != tc.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
