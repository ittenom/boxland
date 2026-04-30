package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"boxland/server/internal/updater"
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

// readyModel returns a sized model that's already past the loading
// splash. Existing tests are not concerned with the first-run prompt,
// so we clear that state here; the dedicated first-run tests below set
// it explicitly.
func readyModel(t *testing.T) model {
	t.Helper()
	m := newModel()
	m.firstRunMissing = nil
	m.firstRunDone = true
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 110, Height: 50})
	return m
}

func testUpdateAvailable() *updater.Status {
	return &updater.Status{Current: "0.1.0", Latest: "0.2.0", HasUpdate: true}
}

// itemNamed pulls the named entry from defaultItems(), so tests don't
// hard-code its position (which shifts when the menu grows).
func itemNamed(t *testing.T, title string) item {
	t.Helper()
	for _, it := range defaultItems() {
		if it.title == title {
			return it
		}
	}
	t.Fatalf("defaultItems() has no %q", title)
	return item{}
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
	if !strings.Contains(out, "Boxland TUI") {
		t.Error("View missing Boxland TUI header")
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
	check := itemNamed(t, checkInstallationTitle)
	m.jobs[check.title] = &job{id: check.title, it: check,
		started: time.Now().Add(-1500 * time.Millisecond)}

	m, _ = step(t, m, runDoneMsg{jobID: check.title, elapsed: 1500 * time.Millisecond})
	if _, still := m.jobs[check.title]; still {
		t.Error("job map should not contain finished job")
	}

	out := m.list.View()
	// The status bar sometimes truncates the toast on narrow widths
	// — assert on a stable prefix (the success mark + the start of
	// the title) instead of the full "Check Installation completed"
	// string.
	if !strings.Contains(out, "✓") || !strings.Contains(out, "Check Installation") {
		t.Errorf("status toast missing success mark or title in list view; got:\n%s", out)
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

func TestRebootKeyArmsCurrentServerJob(t *testing.T) {
	m := readyModel(t)
	design := item{
		title: "Design", badge: "quick start", desc: "test server",
		cmd:        []string{"go", "version"},
		indefinite: true,
	}
	cancelled := false
	live := &job{
		id:     design.title,
		it:     design,
		runner: &runner{cancel: func() { cancelled = true }},
	}
	m.jobs[design.title] = live
	m.currentIndefinite = live

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if !cancelled {
		t.Fatal("reboot key must cancel the current server process")
	}
	if !live.cancelArmed || !live.rebooting {
		t.Fatalf("reboot key must arm reboot state; got cancelArmed=%v rebooting=%v", live.cancelArmed, live.rebooting)
	}
}

func TestRebootStartsFreshServerAfterOldJobExits(t *testing.T) {
	m := readyModel(t)
	design := item{
		title: "Design", badge: "quick start", desc: "test server",
		cmd:        []string{"go", "version"},
		indefinite: true,
	}
	old := &job{
		id:          design.title,
		it:          design,
		cancelArmed: true,
		rebooting:   true,
	}
	m.jobs[design.title] = old
	m.currentIndefinite = old

	m, _ = step(t, m, runDoneMsg{jobID: design.title, err: errors.New("killed"), elapsed: time.Second})
	fresh, ok := m.jobs[design.title]
	if !ok {
		t.Fatal("reboot should start a fresh server job")
	}
	if fresh == old {
		t.Fatal("reboot should replace the old job")
	}
	if m.currentIndefinite != fresh {
		t.Fatal("fresh server job should become the current indefinite job")
	}
	m = drainToCompletion(t, m, design.title)
}

func TestRebootUsesSourceRunWhenRepoRootIsAvailable(t *testing.T) {
	root := t.TempDir()
	t.Chdir(filepath.Join(root))
	if err := os.MkdirAll(filepath.Join(root, "server"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server", "go.mod"), []byte("module probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reboot := sourceRebootItem(item{title: "Design", cmd: []string{"boxland", "design"}})
	want := []string{"go", "-C", root, "run", "./server", "design"}
	if strings.Join(reboot.cmd, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("reboot cmd = %#v, want %#v", reboot.cmd, want)
	}
}

func TestUpdateStopsRunningServerThenRestartsAfterSuccess(t *testing.T) {
	m := readyModel(t)
	design := item{
		title: "Design", badge: "quick start", desc: "test server",
		cmd:        []string{"go", "version"},
		indefinite: true,
	}
	cancelled := false
	live := &job{
		id:     design.title,
		it:     design,
		runner: &runner{cancel: func() { cancelled = true }},
	}
	m.jobs[design.title] = live
	m.currentIndefinite = live
	m.updateStatus = testUpdateAvailable()
	m.refreshMenuItems()

	updateIdx := itemIndex(m.list.Items(), updateBoxlandTitle)
	if updateIdx < 0 {
		t.Fatal("update row missing")
	}
	m.list.Select(updateIdx)
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !cancelled {
		t.Fatal("starting update should stop the running server")
	}
	if !live.cancelArmed || !live.rebooting {
		t.Fatalf("server should be marked as stopping for update; cancelArmed=%v rebooting=%v", live.cancelArmed, live.rebooting)
	}
	if live.afterStop.title != updateBoxlandTitle {
		t.Fatalf("server exit should launch update, got afterStop=%q", live.afterStop.title)
	}
	if m.restartAfterUpdate.title != design.title {
		t.Fatalf("restartAfterUpdate = %q, want %q", m.restartAfterUpdate.title, design.title)
	}

	m, _ = step(t, m, runDoneMsg{jobID: design.title, err: errors.New("killed"), elapsed: time.Second})
	if _, ok := m.jobs[updateBoxlandTitle]; !ok {
		t.Fatal("update job should start after server exits")
	}
	if m.currentIndefinite != nil {
		t.Fatal("server should not restart before update completes")
	}

	m, _ = step(t, m, runDoneMsg{jobID: updateBoxlandTitle, elapsed: time.Second})
	fresh, ok := m.jobs[design.title]
	if !ok {
		t.Fatal("server should restart after successful update")
	}
	if fresh == live {
		t.Fatal("server restart should create a fresh job")
	}
	if m.currentIndefinite != fresh {
		t.Fatal("restarted server should become currentIndefinite")
	}
	m = drainToCompletion(t, m, design.title)
}

func TestUpdateDoesNotRestartServerAfterFailure(t *testing.T) {
	m := readyModel(t)
	design := item{
		title: "Design", badge: "quick start", desc: "test server",
		cmd:        []string{"go", "version"},
		indefinite: true,
	}
	m.restartAfterUpdate = design
	m.jobs[updateBoxlandTitle] = &job{
		id:      updateBoxlandTitle,
		it:      updateBoxlandItem(testUpdateAvailable()),
		started: time.Now(),
	}

	m, _ = step(t, m, runDoneMsg{jobID: updateBoxlandTitle, err: errors.New("update failed"), elapsed: time.Second})
	if _, ok := m.jobs[design.title]; ok {
		t.Fatal("server should not restart after failed update")
	}
	if m.restartAfterUpdate.title != "" {
		t.Fatal("restartAfterUpdate should be cleared after update failure")
	}
}

func TestRebootKeyIgnoresNonServerIndefinite(t *testing.T) {
	m := readyModel(t)
	probe := item{
		title: "Probe", badge: "smoke", desc: "long-running probe",
		cmd:        []string{"go", "version"},
		indefinite: true,
	}
	cancelled := false
	live := &job{
		id:     probe.title,
		it:     probe,
		runner: &runner{cancel: func() { cancelled = true }},
	}
	m.jobs[probe.title] = live
	m.currentIndefinite = live

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cancelled || live.cancelArmed || live.rebooting {
		t.Fatalf("non-server jobs should not reboot; cancelled=%v cancelArmed=%v rebooting=%v", cancelled, live.cancelArmed, live.rebooting)
	}
}

// TestCtrlCWithNoJobsQuits — ctrl+c at the menu when nothing is running
// should quit the TUI.
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
	m.jobs["Design"] = &job{id: "Design", it: itemNamed(t, "Design")}
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
	live := &job{id: "Design", it: itemNamed(t, "Design")}
	m.jobs["Design"] = live
	m.currentIndefinite = live

	// Try to launch Serve (also indefinite).
	serveItem := itemNamed(t, "Serve")
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

	live := &job{id: "Design", it: itemNamed(t, "Design")}
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
		"":                        false,
		"INFO postgres connected": false,
		"time=... level=INFO msg=\"http listening\" addr=:80":    true,
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

	live := &job{id: "Design", it: itemNamed(t, "Design")}
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
	live := &job{id: "Design", it: itemNamed(t, "Design")}
	m.jobs["Design"] = live
	m.currentIndefinite = live

	quick := &job{id: "Migrate", it: itemNamed(t, "Migrate")}
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

// firstRunIntroSnippet is a stable substring of the first-run card's
// intro paragraph. Centralised so tests don't break every time we
// tweak the copy.
const firstRunIntroSnippet = "installation check before you can design"

// TestFirstRunCardRendersWhenMissing confirms the friendly card
// appears in place of the menu when setup hasn't been run, and it
// names every missing item.
func TestFirstRunCardRendersWhenMissing(t *testing.T) {
	m := readyModel(t)
	m.firstRunMissing = []string{"fonts", "templ views"}
	m.firstRunDone = false

	out := m.View()
	if !strings.Contains(out, firstRunIntroSnippet) {
		t.Errorf("first-run card missing intro snippet; got:\n%s", out)
	}
	for _, name := range m.firstRunMissing {
		if !strings.Contains(out, name) {
			t.Errorf("first-run card missing item %q; got:\n%s", name, out)
		}
	}
	// The normal menu title shouldn't be there while the card is up.
	if strings.Contains(out, "Choose your next step") {
		t.Errorf("menu title leaked through first-run card; got:\n%s", out)
	}
}

// TestFirstRunCardHidesAfterDismiss — Tab dismisses the card without
// running the install, returning the user to the normal menu.
func TestFirstRunCardHidesAfterDismiss(t *testing.T) {
	m := readyModel(t)
	m.firstRunMissing = []string{"fonts"}
	m.firstRunDone = false

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.firstRunDone {
		t.Fatal("Tab must dismiss the first-run card")
	}
	out := m.View()
	if strings.Contains(out, firstRunIntroSnippet) {
		t.Errorf("card still visible after Tab; got:\n%s", out)
	}
}

// TestFirstRunCardQuitsOnQ — q from the card quits the TUI cleanly.
func TestFirstRunCardQuitsOnQ(t *testing.T) {
	m := readyModel(t)
	m.firstRunMissing = []string{"fonts"}
	m.firstRunDone = false

	_, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected Quit cmd from first-run card on q")
	}
}

// TestFirstRunCardLaunchesCheckOnS — pressing S kicks off the Check
// Installation item via the regular job machinery.
func TestFirstRunCardLaunchesCheckOnS(t *testing.T) {
	m := readyModel(t)
	m.firstRunMissing = []string{"fonts"}
	m.firstRunDone = false

	// Replace the Check Installation item with a trivial command so
	// we don't actually invoke brew/winget/sqlc/flatc in the test.
	items := m.list.Items()
	for i, raw := range items {
		if it, ok := raw.(item); ok && it.title == checkInstallationTitle {
			it.cmd = []string{"go", "version"}
			items[i] = it
			break
		}
	}
	m.list.SetItems(items)

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if !m.firstRunDone {
		t.Error("S press must hide the first-run card")
	}
	if _, ok := m.jobs[checkInstallationTitle]; !ok {
		t.Fatalf("S press must register a %s job", checkInstallationTitle)
	}
	m = drainToCompletion(t, m, checkInstallationTitle)
}

// TestDesignFirstWhenInstallComplete — once the install is complete,
// Design moves to position 0 (the daily-driver entry point) and is
// the featured row. Check Installation drops to position 1, available
// for re-running after a `git pull` but no longer the focal point.
func TestDesignFirstWhenInstallComplete(t *testing.T) {
	items := itemsForState(true, nil)
	if len(items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(items))
	}
	if items[0].title != "Design" {
		t.Errorf("position 0: want Design, got %q", items[0].title)
	}
	if !items[0].featured {
		t.Error("Design should be featured when install is complete")
	}
	if items[1].title != checkInstallationTitle {
		t.Errorf("position 1: want %q, got %q",
			checkInstallationTitle, items[1].title)
	}
	if items[1].featured {
		t.Errorf("%s should not be featured once install is complete",
			checkInstallationTitle)
	}
}

// TestCheckFirstWhenIncomplete — fresh clone (or post-pull with stale
// generators) gets the install-check at position 0, featured, with
// Design dimmed in the second slot.
func TestCheckFirstWhenIncomplete(t *testing.T) {
	items := itemsForState(false, nil)
	if items[0].title != checkInstallationTitle {
		t.Errorf("position 0: want %q, got %q",
			checkInstallationTitle, items[0].title)
	}
	if !items[0].featured {
		t.Errorf("%s should be featured when install is incomplete",
			checkInstallationTitle)
	}
	if items[1].title != "Design" {
		t.Errorf("position 1: want Design, got %q", items[1].title)
	}
	if items[1].featured {
		t.Error("Design should not be featured until install is complete")
	}
}

// TestCheckInstallationItemPresent — the merged menu entry is wired in.
func TestCheckInstallationItemPresent(t *testing.T) {
	found := false
	for _, it := range defaultItems() {
		if it.title == checkInstallationTitle {
			found = true
			if it.badge != "setup" {
				t.Errorf("%s badge = %q, want %q",
					checkInstallationTitle, it.badge, "setup")
			}
			if !it.interactive {
				t.Errorf("%s should be interactive (brew/sudo prompts)",
					checkInstallationTitle)
			}
			if got := strings.Join(it.cmd, " "); got != "boxland install" {
				t.Errorf("%s cmd = %q, want %q",
					checkInstallationTitle, got, "boxland install")
			}
		}
	}
	if !found {
		t.Errorf("defaultItems() must include a %q entry", checkInstallationTitle)
	}
	// Belt-and-braces: the old separate Install / Setup entries must
	// not still be present after the merge.
	for _, it := range defaultItems() {
		if it.title == "Install" {
			t.Errorf("legacy Install item still in defaultItems()")
		}
		if it.title == "Setup" {
			t.Errorf("legacy Setup item still in defaultItems()")
		}
	}
}

// TestFailureCardShownOnInteractiveFailure — when an interactive job
// exits non-zero with captured tail, the TUI raises a failure card
// instead of letting bubbletea's tea.ExecProcess wipe the trailing
// output unseen.
func TestFailureCardShownOnInteractiveFailure(t *testing.T) {
	m := readyModel(t)
	it := item{title: "Install", badge: "setup", interactive: true,
		cmd: []string{"go", "version"}}
	m.jobs["Install"] = &job{id: "Install", it: it,
		capture: newCaptureBuffer(50)}

	m, _ = step(t, m, runDoneMsg{
		jobID:   "Install",
		err:     errStub("exit status 1"),
		elapsed: 3500 * time.Millisecond,
		tail:    []string{"npm: command not found", "exit status 127"},
	})
	if m.failedJob == nil {
		t.Fatal("expected failedJob to be set after interactive failure with tail")
	}
	out := m.View()
	if !strings.Contains(out, "Install failed") {
		t.Errorf("failure card missing title; got:\n%s", out)
	}
	if !strings.Contains(out, "npm: command not found") {
		t.Errorf("failure card missing tail line; got:\n%s", out)
	}
}

// TestFailureCardSkippedWhenTailEmpty — empty tail falls back to the
// regular toast (the binary couldn't even start, so there's nothing
// useful to show). Avoids rendering an empty card.
func TestFailureCardSkippedWhenTailEmpty(t *testing.T) {
	m := readyModel(t)
	it := item{title: "Install", badge: "setup", interactive: true,
		cmd: []string{"nonsuch"}}
	m.jobs["Install"] = &job{id: "Install", it: it}

	m, _ = step(t, m, runDoneMsg{
		jobID:   "Install",
		err:     errStub("fork/exec: not found"),
		elapsed: 50 * time.Millisecond,
	})
	if m.failedJob != nil {
		t.Errorf("empty tail should not raise the failure card; got %+v", m.failedJob)
	}
}

// TestFailureCardDismissedByEnter — pressing any non-quit key clears
// the card and returns to the menu.
func TestFailureCardDismissedByEnter(t *testing.T) {
	m := readyModel(t)
	m.failedJob = &failedJobView{
		title: "Install", err: errStub("exit 1"),
		elapsed: 1 * time.Second,
		tail:    []string{"line 1", "line 2"},
	}

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.failedJob != nil {
		t.Error("Enter should dismiss the failure card")
	}
}

// TestFailureCardNotShownForNonInteractive — the captured-pipe runner
// already exposes its rolling tail in the logs pane, so we do NOT
// also raise the modal failure card for non-interactive jobs (would
// be a redundant interruption).
func TestFailureCardNotShownForNonInteractive(t *testing.T) {
	m := readyModel(t)
	it := item{title: "Migrate", badge: "database",
		cmd: []string{"boxland", "migrate", "up"}}
	m.jobs["Migrate"] = &job{id: "Migrate", it: it}

	m, _ = step(t, m, runDoneMsg{
		jobID: "Migrate", err: errStub("exit 1"),
		elapsed: 100 * time.Millisecond,
		tail:    []string{"would never reach this path", "but just in case"},
	})
	if m.failedJob != nil {
		t.Error("non-interactive failures must not raise the failure card")
	}
}

// TestCaptureBufferSplitsLines — the rolling buffer splits writes on
// newlines and preserves a partial trailing line until the next
// write completes it.
func TestCaptureBufferSplitsLines(t *testing.T) {
	b := newCaptureBuffer(10)
	b.Write([]byte("hello\nwor"))
	b.Write([]byte("ld\nfoo"))
	got := b.Lines()
	want := []string{"hello", "world", "foo"}
	if len(got) != len(want) {
		t.Fatalf("Lines len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Lines[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCaptureBufferTrimsCR — Windows-style CRLF should not leave a
// dangling \r at the end of each captured line.
func TestCaptureBufferTrimsCR(t *testing.T) {
	b := newCaptureBuffer(5)
	b.Write([]byte("hello\r\nworld\r\n"))
	got := b.Lines()
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Errorf("CRLF not stripped; got %q", got)
	}
}

// TestCaptureBufferRollsWhenOverMax — once the rolling window fills
// up, oldest lines are evicted from the front.
func TestCaptureBufferRollsWhenOverMax(t *testing.T) {
	b := newCaptureBuffer(3)
	for i := 0; i < 10; i++ {
		b.Write([]byte("line\n"))
	}
	got := b.Lines()
	if len(got) != 3 {
		t.Fatalf("expected 3 lines after rollover, got %d", len(got))
	}
}

// errStub is a tiny stand-in for errors.New that doesn't pull in the
// errors package just for tests that need an error.Error() value.
type errStub string

func (e errStub) Error() string { return string(e) }

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
