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

// drainToCompletion polls the runner of the named job until it emits
// runDoneMsg. Used by tests that want to clean up subprocesses spawned
// by go's own toolchain.
func drainToCompletion(t *testing.T, m model, jobID string) model {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := m.jobs[jobID]
		if !ok {
			return m
		}
		if j.runner == nil {
			return m
		}
		msg := j.runner.poll()()
		m, _ = step(t, m, msg)
	}
	t.Fatalf("job %q did not complete within 10s", jobID)
	return m
}

// TestViewRendersEveryItem exercises the menu-only render path.
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
	// hasJobs is false; layout should still be menu-only.
	if m.hasJobs() {
		t.Error("hasJobs must be false on quit press")
	}
}

// TestEnterStartsRunnerAndSplitsLayout confirms a non-interactive item
// registers a job and the View now contains both menu and logs panes.
func TestEnterStartsRunnerAndSplitsLayout(t *testing.T) {
	m := readyModel(t)

	tinyItem := item{
		title: "Probe", badge: "smoke", desc: "echo style probe",
		cmd:        []string{"go", "version"},
		indefinite: true,
	}
	m.list.SetItems([]list.Item{tinyItem})

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := m.jobs["Probe"]; !ok {
		t.Fatal("expected Probe job to be registered")
	}
	if m.currentIndefinite == nil || m.currentIndefinite.it.title != "Probe" {
		t.Errorf("expected currentIndefinite=Probe, got %+v", m.currentIndefinite)
	}
	// Auto-focus jumps to logs pane on indefinite launch.
	if m.focus != focusLogs {
		t.Errorf("expected focusLogs after launching indefinite, got %v", m.focus)
	}
	// Drain so we don't leak the subprocess.
	m = drainToCompletion(t, m, "Probe")
}

// TestInteractiveItemUsesExecProcess verifies that interactive items get
// a tea.ExecProcess command rather than spawning a captured-pipe runner.
func TestInteractiveItemUsesExecProcess(t *testing.T) {
	m := readyModel(t)

	tinyItem := item{
		title: "Probe", badge: "interactive", desc: "interactive probe",
		cmd:         []string{"go", "version"},
		interactive: true,
	}
	m.list.SetItems([]list.Item{tinyItem})

	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := m.jobs["Probe"]; !ok {
		t.Fatal("expected Probe job to be registered")
	}
	if j := m.jobs["Probe"]; j.runner != nil {
		t.Error("interactive items must not spawn a captured-pipe runner")
	}
	if cmd == nil {
		t.Error("expected a tea.Cmd carrying the ExecProcess command")
	}
}

// TestRunDoneClearsSpotlight confirms that finishing the indefinite job
// clears currentIndefinite and posts a status toast.
func TestRunDoneClearsSpotlight(t *testing.T) {
	m := readyModel(t)
	m.jobs["Install"] = &job{id: "Install", it: defaultItems()[0], started: time.Now().Add(-1500 * time.Millisecond)}

	m, _ = step(t, m, runDoneMsg{jobID: "Install", elapsed: 1500 * time.Millisecond})
	if _, still := m.jobs["Install"]; still {
		t.Error("job map should not contain finished job")
	}

	out := m.list.View()
	if !strings.Contains(out, "Install completed") {
		t.Errorf("status toast missing in list view; got:\n%s", out)
	}
}

// TestRunDoneFailureToastFormat formats the failure toast distinctly
// from the success toast.
func TestRunDoneFailureToastFormat(t *testing.T) {
	got := summaryToast(item{title: "Migrate"}, errors.New("exit status 2"), 4200*time.Millisecond)
	if !strings.Contains(got, "Migrate failed") {
		t.Errorf("failure toast missing 'failed': %q", got)
	}
	if !strings.Contains(got, "0:04.2") {
		t.Errorf("failure toast missing elapsed: %q", got)
	}
}

// TestCancelKeyCancelsCurrentIndefinite exercises the ctrl+c path.
func TestCancelKeyCancelsCurrentIndefinite(t *testing.T) {
	m := readyModel(t)
	tinyItem := item{
		title: "Probe", badge: "smoke", desc: "long-running probe",
		cmd:        []string{"go", "version"},
		indefinite: true,
	}
	m.list.SetItems([]list.Item{tinyItem})

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.currentIndefinite == nil {
		t.Fatal("expected currentIndefinite after enter")
	}

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.currentIndefinite == nil || !m.currentIndefinite.cancelArmed {
		t.Error("ctrl+c during run must arm cancellation on currentIndefinite")
	}
	m = drainToCompletion(t, m, "Probe")
}

// TestCtrlCWithNoJobsQuits — ctrl+c at the menu when nothing is running
// should quit the TLI.
func TestCtrlCWithNoJobsQuits(t *testing.T) {
	m := readyModel(t)
	_, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected Quit cmd on ctrl+c with no jobs running")
	}
}

// TestTabSwitchesFocus confirms tab toggles between menu and logs panes
// only when there's something to look at.
func TestTabSwitchesFocus(t *testing.T) {
	m := readyModel(t)

	// Tab with no jobs is a no-op.
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusMenu {
		t.Error("tab with no jobs should leave focus on menu")
	}

	// Pretend a job is running.
	m.jobs["Design"] = &job{id: "Design", it: defaultItems()[1]}
	m.currentIndefinite = m.jobs["Design"]

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusLogs {
		t.Errorf("first tab: want focusLogs, got %v", m.focus)
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusMenu {
		t.Errorf("second tab: want focusMenu, got %v", m.focus)
	}
}

// TestSecondIndefiniteIsRejected verifies launching another indefinite
// while one is live posts a toast and doesn't start a new job.
func TestSecondIndefiniteIsRejected(t *testing.T) {
	m := readyModel(t)

	// Inject a fake live indefinite without actually shelling out.
	live := &job{id: "Design", it: defaultItems()[1]}
	m.jobs["Design"] = live
	m.currentIndefinite = live

	// Try to launch Serve (also indefinite).
	serveItem := defaultItems()[2]
	if !serveItem.indefinite {
		t.Fatal("test fixture assumption: Serve is indefinite")
	}
	m.list.SetItems([]list.Item{serveItem})

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, started := m.jobs["Serve"]; started {
		t.Error("Serve must not start while Design is indefinite-running")
	}
	if !strings.Contains(m.list.View(), "Stop Design first") {
		t.Errorf("expected reject toast in list view; got:\n%s", m.list.View())
	}
}

// TestQuickJobRunsAlongsideIndefinite — at most one indefinite, but
// quick jobs run in parallel.
func TestQuickJobRunsAlongsideIndefinite(t *testing.T) {
	m := readyModel(t)

	live := &job{id: "Design", it: defaultItems()[1]}
	m.jobs["Design"] = live
	m.currentIndefinite = live

	quick := item{
		title: "Probe", badge: "quick", desc: "quick probe",
		cmd: []string{"go", "version"},
	}
	m.list.SetItems([]list.Item{quick})

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := m.jobs["Probe"]; !ok {
		t.Error("quick job must be allowed to start alongside indefinite")
	}
	if m.currentIndefinite == nil || m.currentIndefinite.id != "Design" {
		t.Error("quick job must not steal the spotlight")
	}
	m = drainToCompletion(t, m, "Probe")
}

// TestServiceLinksDerivedFromAddr — the env-derived helper.
func TestServiceLinksDerivedFromAddr(t *testing.T) {
	t.Setenv("BOXLAND_HTTP_ADDR", ":8080")
	links := ServiceLinks("Design")
	if len(links) != 3 {
		t.Fatalf("want 3 links, got %d (%+v)", len(links), links)
	}
	want := map[string]string{
		"Design tools": "http://localhost:8080/design/login",
		"Game client":  "http://localhost:8080/play/login",
		"Health check": "http://localhost:8080/healthz",
	}
	for _, l := range links {
		if want[l.Label] != l.URL {
			t.Errorf("link %q: got %q, want %q", l.Label, l.URL, want[l.Label])
		}
	}

	// Custom host:port overrides the default localhost.
	t.Setenv("BOXLAND_HTTP_ADDR", "0.0.0.0:9999")
	links = ServiceLinks("Serve")
	for _, l := range links {
		if !strings.HasPrefix(l.URL, "http://localhost:9999/") {
			t.Errorf("0.0.0.0 must normalize to localhost; got %q", l.URL)
		}
	}

	// Items that don't run the HTTP server get no links.
	if got := ServiceLinks("Migrate"); got != nil {
		t.Errorf("non-HTTP item must yield nil links, got %+v", got)
	}
}

// TestDetectListening — the "http listening" line must trigger the pin,
// and unrelated lines must not.
func TestDetectListening(t *testing.T) {
	cases := map[string]bool{
		"":                                                    false,
		"INFO postgres connected":                             false,
		"time=... level=INFO msg=\"http listening\" addr=:80": true,
		`{"level":"INFO","msg":"http listening","addr":":8080"}`: true,
	}
	for line, want := range cases {
		if got := DetectListening(line); got != want {
			t.Errorf("DetectListening(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestServiceLinksPinnedAfterListening — the logs pane shows "waiting…"
// before the listening line and the actual links after.
func TestServiceLinksPinnedAfterListening(t *testing.T) {
	t.Setenv("BOXLAND_HTTP_ADDR", ":8080")
	m := readyModel(t)

	live := &job{id: "Design", it: defaultItems()[1]}
	m.jobs["Design"] = live
	m.currentIndefinite = live

	got := m.renderPinnedServices()
	if !strings.Contains(got, "waiting") {
		t.Errorf("before listening: want 'waiting' in pinned strip, got %q", got)
	}

	// Simulate output that contains the listening marker.
	live.runner = nil // avoid poll() dispatch in appendOutput
	m.appendOutput(runOutputMsg{jobID: "Design", lines: []string{"msg=\"http listening\" addr=:8080"}})
	if !live.listening {
		t.Fatal("listening flag should flip after marker line")
	}
	got = m.renderPinnedServices()
	if !strings.Contains(got, "Design tools") || !strings.Contains(got, "/design/login") {
		t.Errorf("after listening: pinned strip missing link; got:\n%s", got)
	}
}

// TestQuickJobLinesArePrefixed — when a quick job runs alongside the
// indefinite spotlight, its lines are tagged so users can tell what
// emitted what.
func TestQuickJobLinesArePrefixed(t *testing.T) {
	m := readyModel(t)
	live := &job{id: "Design", it: defaultItems()[1]}
	m.jobs["Design"] = live
	m.currentIndefinite = live

	quick := &job{id: "Migrate", it: defaultItems()[5]}
	m.jobs["Migrate"] = quick

	m.appendOutput(runOutputMsg{jobID: "Migrate", lines: []string{"applying 003.sql"}})
	tail := strings.Join(m.tailLines, "\n")
	if !strings.Contains(tail, "[Migrate]") {
		t.Errorf("quick-job line missing [Migrate] prefix; got %q", tail)
	}

	m.appendOutput(runOutputMsg{jobID: "Design", lines: []string{"booting design"}})
	tail = strings.Join(m.tailLines, "\n")
	// Indefinite (spotlight) lines stay untagged.
	if strings.Contains(tail, "[Design]") {
		t.Errorf("indefinite line should not be prefixed; got %q", tail)
	}
}

// TestFormatElapsed verifies the M:SS.t / H:MM:SS rendering for both
// buckets.
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
