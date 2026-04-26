// Package tli renders the Boxland Terminal Launch Interface — the menu
// you see when you run `boxland` with no arguments.
//
// The TLI is built on Charmbracelet's bubbles + lipgloss components:
//
//   - viewport.Model holds the gradient logo header so it stays anchored
//     at the top and gracefully overflows on tiny terminals.
//   - list.Model owns the menu items, with a custom ItemDelegate that
//     renders each row in clean tabular form (no background pills, color
//     applied to text, ▎ as the selection bar).
//   - spinner.Model ticks in the footer to show the program is alive.
//   - stopwatch.Model times the running indefinite job.
//   - list.NewStatusMessage flashes a "✓ done in 1m 23s" toast in the list
//     status bar after a run returns.
//   - viewport.Model (a second one) tails captured stdout/stderr while
//     non-interactive jobs are running.
//
// Layout: while a long-running job is live we split the body into a left
// menu pane and a right logs pane (composed with lipgloss.JoinHorizontal,
// no extra layout dep). At most one *indefinite* job (Design, Serve) can
// run at a time; quick jobs (Migrate, Up, Down, Backup, Test) can run
// alongside it and stream their output into the same pane prefixed with
// "[Title] …" so it's clear what emitted what.
//
// Style cues come from the lipgloss "layout" example: thin underline
// rules, columns aligned without dividers, color carried by foreground
// only.
package tli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"boxland/server/internal/branding"
	"boxland/server/internal/setup"
	"boxland/server/internal/updater"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/stopwatch"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// focus tracks which pane keystrokes route to.
type focus int

const (
	focusMenu focus = iota
	focusLogs
)

// item is a single launchable command in the menu. It satisfies list.Item.
type item struct {
	title string
	badge string
	desc  string
	cmd   []string

	featured    bool // emphasised colour ("Design" quick-start)
	interactive bool // needs a real TTY -> route through tea.ExecProcess
	indefinite  bool // runs until cancelled (serve, design loop)
}

func (i item) FilterValue() string {
	return i.title + " " + i.badge + " " + i.desc + " " + strings.Join(i.cmd, " ")
}

// job tracks one in-flight subprocess. We keep finished jobs around just
// long enough to flash a toast on the next Update; appendTail prefixes
// lines from non-indefinite jobs with the title so it's obvious in the
// shared logs pane which command emitted what.
type job struct {
	id          string
	it          item
	runner      *runner        // nil for jobs run via tea.ExecProcess
	capture     *captureBuffer // non-nil for interactive jobs (tee'd output)
	started     time.Time
	cancelArmed bool
	listening   bool // server detected as accepting connections
}

// model wires the bubbles components together.
type model struct {
	list      list.Model
	spinner   spinner.Model
	header    viewport.Model
	tail      viewport.Model
	stopwatch stopwatch.Model

	width  int
	height int
	ready  bool
	focus  focus

	// Active jobs keyed by id (item.title). currentIndefinite, when set,
	// is the one job whose stopwatch and ctrl+c handling drive the run
	// footer; quick jobs are tracked in jobs but don't get the spotlight.
	jobs              map[string]*job
	currentIndefinite *job

	// Aggregated tail across all live and recently-finished jobs. We
	// rebuild from per-job runners on every output batch — cheap because
	// each runner caps its own buffer at tailMaxLines.
	tailLines []string

	// First-run state. When the working tree is missing required
	// build artifacts (fonts, templ output, codegen, ...), the TLI
	// shows a friendly card before the menu and intercepts `s` to run
	// Setup. firstRunDone goes true once the user has either run setup
	// or dismissed the card (pressing Tab/Enter to bypass).
	firstRunMissing []string
	firstRunDone    bool

	// Failure card. When an interactive job exits non-zero, we render
	// a card showing the last lines of its captured output until the
	// user presses Enter. This works around bubbletea's tea.ExecProcess
	// wiping the terminal on resume — without the card, the only sign
	// of failure is a 6-second status-bar toast and the actual error
	// is irretrievable.
	failedJob *failedJobView

	// Update check. updateClient is fired on Init via a tea.Cmd so the
	// menu paints first; the result is stashed in updateStatus and the
	// menu is rebuilt to swap the "Check for updates" item over to a
	// pink "Update Boxland" CTA. nil/zero means we haven't heard back
	// from GitHub (or the user disabled checks via env).
	updateClient *updater.Client
	updateStatus *updater.Status
}

// failedJobView holds just enough about a failed interactive job to
// render a useful post-mortem card.
type failedJobView struct {
	title   string
	err     error
	elapsed time.Duration
	tail    []string
}

// ANSI 256-color palette. We avoid truecolor so the TLI looks consistent
// in Windows PowerShell, macOS Terminal, and Linux ttys alike.
var (
	cPink    = lipgloss.Color("205")
	cMagenta = lipgloss.Color("177")
	cPurple  = lipgloss.Color("141")
	cBlue    = lipgloss.Color("75")
	cCyan    = lipgloss.Color("87")
	cTeal    = lipgloss.Color("51")
	cGreen   = lipgloss.Color("120")
	cYellow  = lipgloss.Color("228")
	cRed     = lipgloss.Color("203")
	cMuted   = lipgloss.Color("244")
	cSubtle  = lipgloss.Color("240")
)

var logoGradient = []lipgloss.Color{cPink, cMagenta, cPurple, cBlue, cCyan, cTeal}

var (
	titleStyle   = lipgloss.NewStyle().Foreground(cPink).Bold(true)
	taglineStyle = lipgloss.NewStyle().Foreground(cMuted)
	dotSep       = lipgloss.NewStyle().Foreground(cSubtle).Render(" · ")

	ruleStyle = lipgloss.NewStyle().Foreground(cSubtle)

	// Per-item title column width. Sized to fit the widest title we
	// render ("Check Installation" = 18) plus 1 cell of breathing
	// room, so descriptions line up across rows.
	nameWidth = 19

	nameUnsel = lipgloss.NewStyle().Width(nameWidth).Bold(true).Foreground(cCyan)
	nameSel   = lipgloss.NewStyle().Width(nameWidth).Bold(true).Foreground(cPink)
	nameFeat  = lipgloss.NewStyle().Width(nameWidth).Bold(true).Foreground(cYellow)
	// nameFeatSel intentionally does NOT add Underline() on top of
	// the yellow+bold treatment. lipgloss's underline pass splits
	// styled text containing spaces into per-character ANSI runs
	// (same bug we hit on the OSC-8 hyperlink in services pinning),
	// which makes the title unsearchable in tests and bloats output
	// ~50x. The pink ▎ chevron already indicates selection clearly.
	nameFeatSel = lipgloss.NewStyle().Width(nameWidth).Bold(true).Foreground(cYellow)

	descStyle = lipgloss.NewStyle().Foreground(cMuted)
	cmdStyle  = lipgloss.NewStyle().Foreground(cGreen)
	chevSel   = lipgloss.NewStyle().Foreground(cPink).Bold(true)

	footerKey    = lipgloss.NewStyle().Foreground(cTeal).Bold(true)
	footerLabel  = lipgloss.NewStyle().Foreground(cMuted)
	spinnerStyle = lipgloss.NewStyle().Foreground(cPink)

	stopwatchStyle = lipgloss.NewStyle().Foreground(cTeal).Bold(true)
	statusOK       = lipgloss.NewStyle().Foreground(cGreen).Bold(true)
	statusErr      = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	tailStyle      = lipgloss.NewStyle().Foreground(cMuted)

	// Logs pane bubble. Rounded border in cSubtle keeps it quiet next to
	// the menu; the title (running item name) carries the colour.
	bubbleBorder       = lipgloss.RoundedBorder()
	bubbleStyle        = lipgloss.NewStyle().Border(bubbleBorder).BorderForeground(cSubtle).Padding(0, 1)
	bubbleStyleFocused = lipgloss.NewStyle().Border(bubbleBorder).BorderForeground(cPink).Padding(0, 1)
	bubbleTitleStyle   = lipgloss.NewStyle().Foreground(cPink).Bold(true)

	// Pinned-services strip styles. Note: we render the URL with a raw
	// ANSI sequence (foreground + underline) rather than a lipgloss
	// style, because lipgloss's word-wrapper splits styled text that
	// contains ESC bytes (the OSC-8 hyperlink wrapper) into per-character
	// runs, which breaks substring assertions and bloats output ~50x.
	pinLabelStyle = lipgloss.NewStyle().Foreground(cCyan).Bold(true)
	pinWaitStyle  = lipgloss.NewStyle().Foreground(cMuted).Italic(true)

	docStyle = lipgloss.NewStyle().Padding(1, 2)
)

// menuPaneWidth is the fixed left-pane width while a job is running. The
// list still gets scrollable rows; pegging this makes the logs pane reflow
// predictably across terminal sizes.
const menuPaneWidth = 52

// delegate is our custom list.ItemDelegate for the menu.
type delegate struct{}

func (delegate) Height() int                             { return 2 }
func (delegate) Spacing() int                            { return 1 }
func (delegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (delegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}
	selected := index == m.Index()

	gutter := "  "
	if selected {
		gutter = chevSel.Render("▎ ")
	}

	nstyle := nameUnsel
	switch {
	case selected && it.featured:
		nstyle = nameFeatSel
	case selected:
		nstyle = nameSel
	case it.featured:
		nstyle = nameFeat
	}
	name := nstyle.Render(it.title)

	gap := " "
	used := lipgloss.Width(gutter) + lipgloss.Width(name) + lipgloss.Width(gap)
	descWidth := m.Width() - used
	if descWidth < 20 {
		descWidth = 20
	}
	desc := descStyle.Render(truncate(it.desc, descWidth))

	headerRow := gutter + name + gap + desc
	indent := strings.Repeat(" ", used)
	// Clip the command row to the list width so a long backup path
	// doesn't blow out the layout when the menu sits beside the logs
	// pane.
	cmdMax := m.Width() - used - 2
	if cmdMax < 12 {
		cmdMax = 12
	}
	cmdRow := indent + cmdStyle.Render("$ "+truncate(strings.Join(it.cmd, " "), cmdMax))

	fmt.Fprint(w, headerRow+"\n"+cmdRow)
}

// checkInstallationItem is the one item that combines what used to be
// Install and Setup. Running it shells out to `boxland install`, which
// already chains brew bootstrap → requirement checks (Docker, Go,
// Node, npm, sqlc, flatc) → npm install → setup (regenerate fonts/
// templ/sqlc/flatc) → build the boxland CLI. So one menu item, one
// subcommand, every step covered.
//
// It stays interactive=true because the brew/winget/sudo/UAC prompts
// underneath need TTY pass-through.
const checkInstallationTitle = "Check Installation"

func checkInstallationItem(featured bool) item {
	return item{
		title:       checkInstallationTitle,
		badge:       "setup",
		desc:        "Install or refresh everything Boxland needs (deps, codegen, fonts).",
		cmd:         []string{"boxland", "install"},
		interactive: true,
		featured:    featured,
	}
}

func designItem(featured bool) item {
	return item{
		title:      "Design",
		badge:      "quick start",
		desc:       "Dependencies, migrations, web build, staging, then serve Boxland.",
		cmd:        []string{"boxland", "design"},
		featured:   featured,
		indefinite: true,
	}
}

// defaultItems is the live menu, with ordering driven by whether the
// install is complete on the current working tree.
func defaultItems() []item {
	return itemsForState(isInstallComplete(), nil)
}

// itemsForState builds the menu with adaptive ordering: when the
// install is complete, Design slots into position 0 (the user's
// daily entry point); when it's incomplete, Check Installation goes
// first and gets the featured/yellow treatment so the eye catches
// it. Split out from defaultItems so tests can assert layout against
// both states without mocking the filesystem.
//
// upd, when non-nil, drives the Update Boxland row: a real "ready"
// CTA when an upstream release is newer than the running version,
// or a quieter "Check for updates" row otherwise. nil means we
// haven't heard back from GitHub yet — the menu still shows the
// row in its check-only form so users know the feature exists.
func itemsForState(installComplete bool, upd *updater.Status) []item {
	check := checkInstallationItem(!installComplete)
	design := designItem(installComplete)
	updateRow := updateBoxlandItem(upd)

	rest := []item{
		{title: "Serve", badge: "server", desc: "Run the Go server only.", cmd: []string{"boxland", "serve"}, indefinite: true},
		{title: "Up", badge: "docker", desc: "Start Postgres, Redis, Mailpit, and MinIO with Docker Compose.", cmd: []string{"boxland", "up"}},
		{title: "Down", badge: "docker", desc: "Stop Docker dependencies.", cmd: []string{"boxland", "down"}},
		{title: "Migrate", badge: "database", desc: "Apply pending SQL migrations.", cmd: []string{"boxland", "migrate", "up"}},
		{title: "Backup", badge: "safety", desc: "Export a complete restore bundle into ./backups.", cmd: []string{"boxland", "backup", "export", defaultBackupPath()}},
		{title: "Restore", badge: "restore", desc: "Restore from ./backups/latest.tar.gz. Destructive; CLI asks you to pass --yes.", cmd: []string{"boxland", "backup", "import", filepath.Join("backups", "latest.tar.gz")}, interactive: true},
		{title: "Test", badge: "quality", desc: "Run Go, web, scripts, and realm isolation tests.", cmd: []string{"boxland", "test"}},
	}

	switch {
	case installComplete && upd != nil && upd.HasUpdate:
		// Update is the *most important* thing the user can do
		// today: their schema is about to drift from upstream. Pin
		// it to the top with the featured highlight — Design slides
		// down one row, but the user still has it instantly via "/".
		return append([]item{updateRow, design, check}, rest...)
	case installComplete:
		// Daily-driver layout: Design first, then the install check
		// stays available for re-running after a `git pull`. Update
		// row tucks just below the always-relevant items so power
		// users can still trigger a manual "Check for updates".
		return append([]item{design, check}, append(rest, updateRow)...)
	default:
		// Fresh-clone layout: install first, design dimmed below.
		// We still surface the update row (in check-only form) so a
		// user re-installing after a git pull can confirm they're
		// actually on the newest tag.
		return append([]item{check, design}, append(rest, updateRow)...)
	}
}

// updateBoxlandTitle is the constant title we use to look up the
// update row when refreshing menu items or routing the U hotkey, so
// renames stay in lockstep.
const updateBoxlandTitle = "Update Boxland"

// updateBoxlandItem builds the row in either "ready to apply" or
// "check only" form. When ready, the row carries the new version in
// its description and runs `boxland update`; otherwise it runs
// `boxland update --check` so a user behind a flaky connection can
// retry the GitHub probe by hand.
func updateBoxlandItem(s *updater.Status) item {
	if s != nil && s.HasUpdate {
		desc := fmt.Sprintf("New release %s available — pull, migrate, rebuild, restart.",
			normalizeTag(s.Latest))
		return item{
			title:       updateBoxlandTitle,
			badge:       "ready",
			desc:        desc,
			cmd:         []string{"boxland", "update"},
			interactive: true, // sudoless prompts (git creds, etc.) need TTY
			featured:    true,
		}
	}
	desc := "Ask GitHub if a newer Boxland is available."
	if s != nil && s.Latest != "" {
		desc = fmt.Sprintf("Up to date (latest is %s). Press to re-check.",
			normalizeTag(s.Latest))
	}
	return item{
		title:       updateBoxlandTitle,
		badge:       "update",
		desc:        desc,
		cmd:         []string{"boxland", "update", "--check"},
		interactive: false,
	}
}

// normalizeTag prefixes a `v` if missing so the UI consistently
// shows tag-style versions ("v0.2.0") rather than mixing bare and
// prefixed forms.
func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		return s
	}
	return "v" + s
}

// isInstallComplete decides which menu layout to render. We treat
// installation as complete only when *both*:
//
//   - every required tool is on PATH (so `boxland install` would
//     not need to run any package-manager step), and
//   - setup.Need() reports no missing build artifacts.
//
// Either condition alone is insufficient: a user can have Docker on
// PATH but a missing templ output dir from a fresh `git pull`, and
// we still want the install card to fire.
func isInstallComplete() bool {
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	if missing := setup.Need(wd); len(missing) > 0 {
		return false
	}
	for _, c := range setup.RequiredCmds() {
		if _, err := exec.LookPath(c); err != nil {
			return false
		}
	}
	return true
}

func defaultBackupPath() string {
	return filepath.Join("backups", "boxland-"+time.Now().Format("20060102-150405")+".tar.gz")
}

func newModel() model {
	items := defaultItems()
	li := make([]list.Item, len(items))
	for i := range items {
		li[i] = items[i]
	}

	l := list.New(li, delegate{}, 80, 24)
	l.Title = "Choose your next step"
	l.SetShowStatusBar(true)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.StatusMessageLifetime = 6 * time.Second

	l.Styles.TitleBar = lipgloss.NewStyle().Padding(0, 0, 1, 0)
	l.Styles.Title = lipgloss.NewStyle().Foreground(cPink).Bold(true)
	l.Styles.PaginationStyle = lipgloss.NewStyle().Foreground(cMuted).PaddingLeft(2)
	l.Styles.NoItems = lipgloss.NewStyle().Foreground(cMuted)
	l.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(cTeal)
	l.Styles.FilterCursor = lipgloss.NewStyle().Foreground(cPink)
	l.Styles.DefaultFilterCharacterMatch = lipgloss.NewStyle().Foreground(cYellow).Underline(true)
	l.Styles.ActivePaginationDot = lipgloss.NewStyle().Foreground(cPink).SetString("•")
	l.Styles.InactivePaginationDot = lipgloss.NewStyle().Foreground(cSubtle).SetString("•")
	l.Styles.StatusBar = lipgloss.NewStyle().Foreground(cMuted).Padding(0, 0, 1, 0)
	l.Styles.StatusBarFilterCount = lipgloss.NewStyle().Foreground(cSubtle)
	l.Styles.StatusBarActiveFilter = lipgloss.NewStyle().Foreground(cTeal)
	l.Styles.StatusEmpty = lipgloss.NewStyle().Foreground(cSubtle)

	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = spinnerStyle

	header := viewport.New(80, headerHeight())
	header.SetContent(renderHeader(80))

	tail := viewport.New(80, 12)

	sw := stopwatch.NewWithInterval(100 * time.Millisecond)

	m := model{
		list:         l,
		spinner:      s,
		header:       header,
		tail:         tail,
		stopwatch:    sw,
		focus:        focusMenu,
		jobs:         map[string]*job{},
		updateClient: updater.NewClient(updater.DefaultRepo),
	}
	// Inspect the working tree once at startup. Cwd is the canonical
	// repo root in our docs ("run from the boxland/ directory"), so
	// we don't try to walk up looking for a marker. The featured-row
	// highlight is handled by defaultItems()'s adaptive ordering, so
	// no further work is needed here.
	if wd, err := os.Getwd(); err == nil {
		m.firstRunMissing = setup.Need(wd)
	}
	return m
}

// headerHeight is the number of lines the logo + tagline + rule occupies.
func headerHeight() int {
	logoLines := strings.Count(strings.TrimRight(branding.Logo, "\n"), "\n") + 1
	// logo + blank + tagline + rule
	return logoLines + 3
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.checkForUpdate(false))
}

// updateCheckMsg carries the result of an updater check back to the
// Update loop. status is nil when the check was disabled or had no
// useful answer; the model just keeps showing what it had before.
type updateCheckMsg struct {
	status *updater.Status
}

// checkForUpdate runs the GitHub-Releases check off the bubbletea
// goroutine so the menu renders first. It is intentionally fail-soft:
// any error path returns a nil status which the model then treats as
// "no opinion".
//
// force=true skips the on-disk TTL gate (still respects ETag and the
// in-process throttle) and is used when the user invokes the Check
// for updates menu item explicitly.
func (m model) checkForUpdate(force bool) tea.Cmd {
	c := m.updateClient
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		s, _ := c.CheckLatest(ctx, updater.CheckOpts{ForceRefresh: force})
		return updateCheckMsg{status: s}
	}
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.applySize(msg.Width, msg.Height)

	case runOutputMsg:
		m.appendOutput(msg)
		if j, ok := m.jobs[msg.jobID]; ok && j.runner != nil {
			cmds = append(cmds, j.runner.poll())
		}

	case updateCheckMsg:
		// Keep prior status if the check came back empty (offline,
		// disabled, etc.) — there's no point throwing away a perfectly
		// good cached snapshot just because today's refresh failed.
		if msg.status != nil {
			m.updateStatus = msg.status
			m.refreshMenuItems()
		}

	case runDoneMsg:
		// When Check Installation finishes (success or failure),
		// refresh the first-run state so the friendly card disappears
		// if the user is now ready to design — and rebuild the menu
		// so Design slots into position 0 (its featured "daily
		// driver" home) when the install is complete.
		if msg.jobID == checkInstallationTitle {
			if wd, err := os.Getwd(); err == nil {
				m.firstRunMissing = setup.Need(wd)
				if len(m.firstRunMissing) == 0 {
					m.firstRunDone = true
				}
			}
			m.refreshMenuItems()
		}
		// When Update Boxland finishes, re-check GitHub so the menu
		// either snaps back to its "Up to date" form (success: the
		// new binary will pick up the new VERSION on next launch,
		// but we re-check anyway in case the user updates twice in
		// a row across releases) or stays on "Update available" so
		// the user can retry. Force the refresh because the on-disk
		// TTL would otherwise serve a stale snapshot.
		if msg.jobID == updateBoxlandTitle {
			m, cmd := m.handleRunDone(msg)
			return m, tea.Batch(cmd, m.(model).checkForUpdate(true))
		}
		return m.handleRunDone(msg)

	case runStartFailedMsg:
		return m.handleRunDone(runDoneMsg{jobID: msg.jobID, err: msg.err})

	case tea.KeyMsg:
		// First-run card intercepts everything except quit. The user's
		// only real choices are: check installation (S) or quit.
		if m.showFirstRun() {
			switch {
			case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c", "q", "esc"))):
				return m, tea.Quit
			case key.Matches(msg, key.NewBinding(key.WithKeys("s", "S", "enter"))):
				return m.startCheckInstallation()
			case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
				// Power-user escape hatch: dismiss the card and use
				// the menu directly. We don't clear firstRunMissing
				// (the Check Installation item stays featured) — just
				// hide the card.
				m.firstRunDone = true
				return m, nil
			}
			return m, nil
		}

		// Failure card: any key dismisses it and returns to the menu.
		// Quit shortcuts (ctrl+c/q/esc) still quit; everything else
		// just clears the card so the user can re-try.
		if m.failedJob != nil {
			switch {
			case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
				return m, tea.Quit
			case key.Matches(msg, key.NewBinding(key.WithKeys("q", "esc"))):
				m.failedJob = nil
				return m, tea.Quit
			default:
				m.failedJob = nil
				return m, nil
			}
		}

		// Don't intercept keys while the list's filter input is active.
		filtering := m.focus == focusMenu && m.list.FilterState() == list.Filtering
		if !filtering {
			switch {
			case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
				return m.handleCtrlC()
			case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
				m.toggleFocus()
				return m, nil
			case key.Matches(msg, key.NewBinding(key.WithKeys("shift+tab"))):
				m.toggleFocus()
				return m, nil
			case m.focus == focusMenu && key.Matches(msg, key.NewBinding(key.WithKeys("q", "esc"))):
				if m.list.FilterState() == list.Unfiltered {
					return m, tea.Quit
				}
			case m.focus == focusMenu && key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
				return m.startSelected()
			case m.focus == focusMenu && key.Matches(msg, key.NewBinding(key.WithKeys("u", "U"))):
				// U is the global "go to update" shortcut. When an
				// update is available, it launches `boxland update`
				// directly; otherwise it triggers a fresh GitHub
				// check (skipping the on-disk TTL) and surfaces the
				// result in the menu. Either way it routes through
				// the existing job machinery so the logs pane and
				// status toast behave identically to a click.
				return m.startUpdate()
			}
		}
	}

	// Route arrow/page keys to the focused pane. The menu's list.Model
	// always gets non-key messages (like its own filter/help cmds).
	var cmd tea.Cmd
	switch m.focus {
	case focusLogs:
		// Send keys (and ticks) to the tail viewport.
		m.tail, cmd = m.tail.Update(msg)
		cmds = append(cmds, cmd)
		// Still let the list see non-key messages.
		if _, isKey := msg.(tea.KeyMsg); !isKey {
			m.list, cmd = m.list.Update(msg)
			cmds = append(cmds, cmd)
		}
	default:
		m.list, cmd = m.list.Update(msg)
		cmds = append(cmds, cmd)
		// Tail still ticks (animations, etc.) on non-key messages.
		if _, isKey := msg.(tea.KeyMsg); !isKey {
			m.tail, cmd = m.tail.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	m.spinner, cmd = m.spinner.Update(msg)
	cmds = append(cmds, cmd)

	m.stopwatch, cmd = m.stopwatch.Update(msg)
	cmds = append(cmds, cmd)

	m.header, cmd = m.header.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// showFirstRun reports whether the friendly first-run card should be
// rendered in place of the menu. We hide it the moment the user
// either completes setup or chooses to dismiss it (Tab).
func (m model) showFirstRun() bool {
	return len(m.firstRunMissing) > 0 && !m.firstRunDone
}

// startUpdate is the "U" hotkey handler. When an update is known to
// be available, it selects the Update Boxland row and dispatches as
// if the user had pressed Enter. When no update is known (or we're
// up-to-date), it kicks a forced re-check off the bubbletea
// goroutine and shows a transient toast so the user knows the
// keystroke was received.
func (m model) startUpdate() (tea.Model, tea.Cmd) {
	if m.updateStatus != nil && m.updateStatus.HasUpdate {
		idx := itemIndex(m.list.Items(), updateBoxlandTitle)
		if idx < 0 {
			return m, nil
		}
		m.list.Select(idx)
		return m.startSelected()
	}
	// No update known yet; force-refresh and let the user know we're
	// asking. The toast is short-lived (status-bar default lifetime)
	// so it won't pile up if they mash the key.
	cmds := []tea.Cmd{
		m.list.NewStatusMessage("Checking for updates…"),
		m.checkForUpdate(true),
	}
	return m, tea.Batch(cmds...)
}

// startCheckInstallation launches the Check Installation item from
// the first-run card. It reuses the same job machinery the menu
// uses, so output streams into the logs pane and the user can watch
// progress (and the failure card surfaces any captured errors).
func (m model) startCheckInstallation() (tea.Model, tea.Cmd) {
	idx := itemIndex(m.list.Items(), checkInstallationTitle)
	if idx < 0 {
		return m, nil
	}
	// Hide the card right away so the logs pane has room to breathe,
	// then dispatch as if the user had pressed Enter on the menu row.
	m.firstRunDone = true
	m.list.Select(idx)
	return m.startSelected()
}

// itemIndex returns the position of the named item in the slice, or
// -1 when not found. Callers must guard against a missing item; we
// don't silently return 0 because that would dispatch the wrong row.
func itemIndex(items []list.Item, title string) int {
	for i, raw := range items {
		if it, ok := raw.(item); ok && it.title == title {
			return i
		}
	}
	return -1
}

// refreshMenuItems rebuilds the list from itemsForState, which
// re-evaluates isInstallComplete() and the cached update status, so
// the menu can swap Design ↔ Check Installation and surface a fresh
// "Update Boxland" CTA when GitHub returns a newer release. Called
// after Check Installation or Update finishes, and whenever an
// update check lands a new status.
//
// We preserve the user's selected row across rebuilds when possible
// (matched by title) so the cursor doesn't snap back to the top
// every time a quick job completes.
func (m *model) refreshMenuItems() {
	prev, _ := m.list.SelectedItem().(item)
	items := itemsForState(isInstallComplete(), m.updateStatus)
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	m.list.SetItems(li)
	if prev.title != "" {
		if idx := itemIndex(li, prev.title); idx >= 0 {
			m.list.Select(idx)
			return
		}
	}
	m.list.Select(0)
}

// toggleFocus flips between menu and logs panes. Logs focus is a no-op
// when no jobs are running (there's nothing to scroll).
func (m *model) toggleFocus() {
	if !m.hasJobs() {
		m.focus = focusMenu
		return
	}
	if m.focus == focusMenu {
		m.focus = focusLogs
	} else {
		m.focus = focusMenu
	}
}

// handleCtrlC: cancel the current indefinite job if any (first press =
// graceful, second = the runner's WaitDelay forces a kill); otherwise
// quit the TLI.
func (m model) handleCtrlC() (tea.Model, tea.Cmd) {
	if m.currentIndefinite != nil {
		j := m.currentIndefinite
		if j.runner != nil {
			j.runner.Cancel()
			j.cancelArmed = true
		}
		return m, nil
	}
	return m, tea.Quit
}

// hasJobs reports whether any subprocess is live or any tail content is
// worth showing.
func (m model) hasJobs() bool {
	return len(m.jobs) > 0 || len(m.tailLines) > 0
}

// startSelected validates the request, registers a job, and either spawns
// a captured-pipe runner or hands off to tea.ExecProcess. Quick jobs may
// run in parallel with one indefinite job; a second indefinite or any
// interactive job is rejected with a status-bar toast while a job is
// live.
func (m model) startSelected() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok || len(it.cmd) == 0 {
		return m, nil
	}

	if _, dup := m.jobs[it.title]; dup {
		return m, m.list.NewStatusMessage(it.title + " is already running.")
	}

	if m.currentIndefinite != nil {
		if it.indefinite {
			return m, m.list.NewStatusMessage("Stop " + m.currentIndefinite.it.title + " first (ctrl+c).")
		}
		if it.interactive {
			return m, m.list.NewStatusMessage("Stop " + m.currentIndefinite.it.title + " before running " + it.title + ".")
		}
	}

	bin, args := resolveCmd(it.cmd)

	if it.interactive {
		// Hand the terminal over directly; bubbletea suspends the TUI
		// for the duration of the subprocess and resumes after.
		//
		// We tee the child's stdout/stderr through a rolling capture
		// buffer so a failure card can re-show the last lines after
		// bubbletea wipes the screen on resume. Pre-assigning the
		// fields here is preserved by tea.ExecProcess (it only
		// overrides them when nil — see bubbletea/exec.go's
		// SetStdout/SetStderr).
		j := &job{id: it.title, it: it, started: time.Now(), capture: newCaptureBuffer(tailMaxLines)}
		m.jobs[it.title] = j
		// Re-apply size so the menu shrinks to make room for the
		// logs pane when the TUI redraws after exec returns.
		m.applySize(m.width, m.height)
		c := exec.Command(bin, args...)
		c.Stdout = io.MultiWriter(os.Stdout, j.capture)
		c.Stderr = io.MultiWriter(os.Stderr, j.capture)
		execCmd := tea.ExecProcess(c, func(err error) tea.Msg {
			return runDoneMsg{
				jobID:   it.title,
				err:     err,
				elapsed: time.Since(j.started),
				tail:    j.capture.Lines(),
			}
		})
		return m, execCmd
	}

	r, pollCmd, err := startRunner(it.title, bin, args)
	if err != nil {
		return m, func() tea.Msg { return runStartFailedMsg{jobID: it.title, err: err} }
	}
	j := &job{id: it.title, it: it, runner: r, started: time.Now()}
	m.jobs[it.title] = j

	// Now that hasJobs() reflects the new job, re-apply size so the
	// menu shrinks to make room for the logs pane.
	m.applySize(m.width, m.height)

	var extra []tea.Cmd
	if it.indefinite {
		m.currentIndefinite = j
		// Reset + start the stopwatch for the new spotlight job.
		extra = append(extra, m.stopwatch.Reset(), m.stopwatch.Start())
		// Auto-focus the logs pane so the user sees output immediately.
		m.focus = focusLogs
	}
	extra = append(extra, pollCmd)
	return m, tea.Batch(extra...)
}

// resolveCmd substitutes the calling executable for any literal "boxland"
// in the cmd, so the chosen step works under `go run`, `boxland.cmd`, and
// installed binaries alike.
func resolveCmd(cmd []string) (string, []string) {
	bin := cmd[0]
	if bin == "boxland" {
		if exe, err := os.Executable(); err == nil {
			bin = exe
		}
	}
	return bin, cmd[1:]
}

// ---------------------------------------------------------------------------
// Output handling
// ---------------------------------------------------------------------------

// appendOutput merges the new batch into the aggregated tail, prefixing
// quick-job lines with the job title, and detects the listening marker
// for service-URL pinning.
func (m *model) appendOutput(msg runOutputMsg) {
	j, ok := m.jobs[msg.jobID]
	if !ok || len(msg.lines) == 0 {
		return
	}
	prefix := ""
	// Prefix lines from quick jobs running alongside an indefinite one
	// so users can tell what's printing what.
	if !j.it.indefinite && m.currentIndefinite != nil {
		prefix = lipgloss.NewStyle().Foreground(cTeal).Render("["+j.it.title+"] ") + " "
	}
	for _, line := range msg.lines {
		if !j.listening && j.it.indefinite && DetectListening(line) {
			j.listening = true
		}
		m.tailLines = append(m.tailLines, prefix+line)
	}
	if len(m.tailLines) > tailMaxLines {
		m.tailLines = m.tailLines[len(m.tailLines)-tailMaxLines:]
	}
	m.tail.SetContent(tailStyle.Render(strings.Join(m.tailLines, "\n")))
	m.tail.GotoBottom()
}

func (m model) handleRunDone(msg runDoneMsg) (tea.Model, tea.Cmd) {
	j, ok := m.jobs[msg.jobID]
	if !ok {
		return m, nil
	}
	delete(m.jobs, msg.jobID)

	// For indefinite items the user explicitly cancelled, an error is
	// expected and we shouldn't shout about it.
	err := msg.err
	if j.it.indefinite && j.cancelArmed {
		err = nil
	}

	// Failed interactive jobs get the failure card treatment. We only
	// trigger it when there's actually captured output to show; an
	// empty tail (e.g. the binary couldn't even start) falls back to
	// the toast.
	if err != nil && j.it.interactive && len(msg.tail) > 0 {
		m.failedJob = &failedJobView{
			title:   j.it.title,
			err:     err,
			elapsed: msg.elapsed,
			tail:    msg.tail,
		}
	}

	toast := summaryToast(j.it, err, msg.elapsed)
	cmds := []tea.Cmd{m.list.NewStatusMessage(toast)}

	if m.currentIndefinite != nil && m.currentIndefinite.id == msg.jobID {
		m.currentIndefinite = nil
		cmds = append(cmds, m.stopwatch.Stop(), m.stopwatch.Reset())
		// Drop logs focus once the spotlight job ends and there's
		// nothing left to read interactively.
		if !m.hasJobs() {
			m.focus = focusMenu
		}
	}

	// Re-apply size so the menu re-expands now that hasJobs() may
	// have flipped to false (or the surviving job set changed).
	m.applySize(m.width, m.height)

	return m, tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// Layout / sizing
// ---------------------------------------------------------------------------

func (m *model) applySize(w, h int) {
	m.width, m.height = w, h
	m.ready = true
	contentWidth := w - 4
	if contentWidth < 64 {
		contentWidth = 64
	}

	m.header.Width = contentWidth
	m.header.Height = headerHeight()
	m.header.SetContent(renderHeader(contentWidth))

	bodyHeight := h - headerHeight() - 5
	if bodyHeight < 10 {
		bodyHeight = 10
	}

	// Menu pane sizing. When a long-running job is live we need to
	// share horizontal space with the logs pane, so we clamp to
	// menuPaneWidth on wide terminals. When nothing's running the
	// list gets the full width, which lets descriptions breathe.
	listWidth := contentWidth
	if m.hasJobs() && contentWidth >= menuPaneWidth+30 {
		listWidth = menuPaneWidth
	}
	m.list.SetSize(listWidth, bodyHeight)

	// Tail viewport sits inside a 1-cell rounded border with 1 col of
	// padding on each side, plus a 2-line title block above. We size
	// the inner viewport so the bordered bubble matches the list.
	tailWidth := contentWidth - listWidth - 1
	if tailWidth < 30 {
		tailWidth = 30
	}
	m.tail.Width = tailWidth - 4 // border (2) + padding (2)
	if m.tail.Width < 10 {
		m.tail.Width = 10
	}
	m.tail.Height = bodyHeight - 4 // border + title area
	if m.tail.Height < 4 {
		m.tail.Height = 4
	}
}

// ---------------------------------------------------------------------------
// Views
// ---------------------------------------------------------------------------

func (m model) View() string {
	if !m.ready {
		return "\n  " + m.spinner.View() + " Loading Boxland…\n"
	}

	body := m.viewBody()
	switch {
	case m.failedJob != nil:
		body = m.renderFailureCard()
	case m.showFirstRun() && !m.hasJobs():
		body = m.renderFirstRunCard()
	}
	footer := m.renderFooter(m.header.Width)

	return docStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		m.header.View(),
		body,
		footer,
	))
}

// renderFirstRunCard is the friendly pre-menu view the TLI shows on a
// fresh clone. It explains in plain English what's missing and how to
// fix it with one keystroke.
func (m model) renderFirstRunCard() string {
	width := m.header.Width
	cardWidth := width
	if cardWidth > 76 {
		cardWidth = 76
	}

	titleLine := bubbleTitleStyle.Render("Welcome to Boxland")
	intro := descStyle.Render("Boxland needs a quick installation check before you can design. " +
		"Press S to run it now, or q to quit.")

	missingHeader := footerLabel.Render("Still required:")
	rows := make([]string, 0, len(m.firstRunMissing))
	for _, name := range m.firstRunMissing {
		rows = append(rows, "  "+statusErr.Render("•")+" "+descStyle.Render(name))
	}

	hint := footerKey.Render("S") + footerLabel.Render(" check installation   ") +
		footerKey.Render("q") + footerLabel.Render(" quit")

	parts := []string{titleLine, "", intro, "", missingHeader}
	parts = append(parts, rows...)
	parts = append(parts, "", hint)

	card := bubbleStyleFocused.
		Width(cardWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))

	// Centre the card inside the body width so it doesn't hug the
	// left margin on wide terminals.
	pad := (width - lipgloss.Width(card)) / 2
	if pad < 0 {
		pad = 0
	}
	leftPad := strings.Repeat(" ", pad)
	lines := strings.Split(card, "\n")
	for i, ln := range lines {
		lines[i] = leftPad + ln
	}
	return strings.Join(lines, "\n")
}

// renderFailureCard shows the captured tail of a failed interactive
// job so the user can read the error after bubbletea wipes the
// terminal on tea.ExecProcess resume.
//
// The card is intentionally generous on size: the user's reading,
// not navigating. We show up to 40 trailing lines and leave the
// menu hidden until they press a key — that's safer than racing the
// TUI redraw to flash the error.
func (m model) renderFailureCard() string {
	if m.failedJob == nil {
		return ""
	}
	width := m.header.Width
	cardWidth := width
	if cardWidth > 100 {
		cardWidth = 100
	}

	fj := m.failedJob
	titleLine := statusErr.Render("✗ ") + bubbleTitleStyle.Render(fj.title+" failed")
	subtitle := footerLabel.Render(fmt.Sprintf("after %s · %s",
		formatElapsed(fj.elapsed), errMessage(fj.err)))

	tail := fj.tail
	const maxLines = 40
	if len(tail) > maxLines {
		tail = tail[len(tail)-maxLines:]
	}
	body := tailStyle.Render(strings.Join(tail, "\n"))

	hint := footerKey.Render("Enter") + footerLabel.Render(" return to menu   ") +
		footerKey.Render("q") + footerLabel.Render(" quit")

	parts := []string{titleLine, subtitle, "", body, "", hint}

	card := bubbleStyleFocused.
		Width(cardWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))

	pad := (width - lipgloss.Width(card)) / 2
	if pad < 0 {
		pad = 0
	}
	leftPad := strings.Repeat(" ", pad)
	lines := strings.Split(card, "\n")
	for i, ln := range lines {
		lines[i] = leftPad + ln
	}
	return strings.Join(lines, "\n")
}

// viewBody composes the always-visible menu pane with the optional logs
// pane. The two are joined horizontally — no extra layout dep needed.
//
// When an update is available, a one-line banner sits above the body so
// users notice it on TLI launch even before they look at the menu.
func (m model) viewBody() string {
	menu := m.list.View()
	body := menu
	if m.hasJobs() {
		logs := m.renderLogsPane()
		body = lipgloss.JoinHorizontal(lipgloss.Top, menu, " ", logs)
	}

	if banner := m.renderUpdateBanner(); banner != "" {
		return lipgloss.JoinVertical(lipgloss.Left, banner, body)
	}
	return body
}

// renderUpdateBanner is the slim "✦ Update available" strip that lives
// above the menu when the cached update status flags a newer release.
// Returns "" when there's nothing to say (no check yet, up-to-date,
// or BOXLAND_DISABLE_UPDATE_CHECK=true). Color-coded with the same
// pink/cyan palette as the rest of the TLI so it's instantly
// recognisable as part of the family.
func (m model) renderUpdateBanner() string {
	s := m.updateStatus
	if s == nil || !s.HasUpdate {
		return ""
	}
	width := m.header.Width
	if width <= 0 {
		return ""
	}
	icon := lipgloss.NewStyle().Foreground(cPink).Bold(true).Render("✦")
	label := lipgloss.NewStyle().Foreground(cPink).Bold(true).Render("Update available")
	jump := lipgloss.NewStyle().Foreground(cMuted).Render(
		normalizeTag(s.Current) + " → " + normalizeTag(s.Latest))
	hint := footerKey.Render("U") + footerLabel.Render(" update now")

	left := icon + " " + label + dotSep + jump
	gap := width - lipgloss.Width(left) - lipgloss.Width(hint)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + hint + "\n"
}

// renderLogsPane builds the right-hand bubble with a pinned services
// strip on top of the streaming tail viewport.
func (m model) renderLogsPane() string {
	bubble := bubbleStyle
	if m.focus == focusLogs {
		bubble = bubbleStyleFocused
	}

	title := m.renderLogsTitle()
	pinned := m.renderPinnedServices()
	tail := m.tail.View()
	if strings.TrimSpace(tail) == "" {
		tail = footerLabel.Render("(waiting for output…)")
	}

	parts := []string{title}
	if pinned != "" {
		parts = append(parts, pinned)
	}
	parts = append(parts, renderRule(m.tail.Width), tail)

	return bubble.
		Width(m.tail.Width + 2).
		Height(m.tail.Height + 4).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// renderLogsTitle shows the spotlight job's title + badge + stopwatch on
// the first line of the bubble.
func (m model) renderLogsTitle() string {
	if m.currentIndefinite == nil {
		// At least one quick job is live — show "Logs" plus a little
		// summary of who's running.
		titles := make([]string, 0, len(m.jobs))
		for _, j := range m.jobs {
			titles = append(titles, j.it.title)
		}
		sort.Strings(titles)
		left := bubbleTitleStyle.Render("Logs")
		right := footerLabel.Render(strings.Join(titles, " · "))
		return alignBetween(left, right, m.tail.Width)
	}
	j := m.currentIndefinite
	nameStyle := nameUnsel
	if j.it.featured {
		nameStyle = nameFeat
	}
	left := chevSel.Render("◆ ") + nameStyle.Render(j.it.title)
	right := stopwatchStyle.Render(formatElapsed(m.stopwatch.Elapsed()))
	return alignBetween(left, right, m.tail.Width)
}

// renderPinnedServices is the strip of HTTP service URLs at the top of
// the logs bubble. Returns "" when no service-URLs apply (e.g. the
// running job isn't Design/Serve, or nothing is running).
func (m model) renderPinnedServices() string {
	if m.currentIndefinite == nil {
		return ""
	}
	j := m.currentIndefinite
	links := ServiceLinks(j.it.title)
	if len(links) == 0 {
		return ""
	}
	if !j.listening {
		return pinWaitStyle.Render("waiting for server…")
	}
	rows := make([]string, 0, len(links))
	for _, l := range links {
		row := pinLabelStyle.Render(padOrTruncate(l.Label, 14)) +
			"  " +
			styledHyperlink(l.URL)
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// styledHyperlink emits an OSC-8 hyperlink whose visible text is colored
// + underlined via raw ANSI SGR (94 = bright blue, 4 = underline). We use
// raw codes here, not lipgloss, because lipgloss's word-wrapper interacts
// poorly with the OSC-8 escape (it splits styled text into per-char runs).
func styledHyperlink(url string) string {
	const sgrOn = "\x1b[4;94m"
	const sgrOff = "\x1b[0m"
	return "\x1b]8;;" + url + "\x1b\\" + sgrOn + url + sgrOff + "\x1b]8;;\x1b\\"
}

// alignBetween renders left + spaces + right exactly width cells wide,
// preserving lipgloss styling. Used for run header and logs title.
func alignBetween(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func renderHeader(width int) string {
	logo := renderLogo()
	titleRow := lipgloss.JoinHorizontal(lipgloss.Top,
		titleStyle.Render("Boxland TLI"),
		dotSep,
		taglineStyle.Render("Terminal Launch Interface"),
	)
	return lipgloss.JoinVertical(lipgloss.Left,
		logo,
		"",
		titleRow,
		renderRule(width),
	)
}

// renderRule draws a 1-line horizontal rule that's exactly width cells wide.
func renderRule(width int) string {
	if width < 1 {
		return ""
	}
	return ruleStyle.Render(strings.Repeat("─", width))
}

func renderLogo() string {
	lines := strings.Split(strings.TrimRight(branding.Logo, "\n"), "\n")
	out := make([]string, len(lines))
	for i, line := range lines {
		c := logoGradient[i%len(logoGradient)]
		out[i] = lipgloss.NewStyle().Foreground(c).Bold(true).Render(line)
	}
	return strings.Join(out, "\n")
}

// renderFooter shows different hint sets depending on whether jobs are
// running. We keep the same key vocabulary across menu and logs so the
// muscle memory carries over.
func (m model) renderFooter(width int) string {
	hint := func(k, label string) string {
		return footerKey.Render(k) + footerLabel.Render(" "+label)
	}

	var hintRow string
	if !m.hasJobs() {
		hintRow = strings.Join([]string{
			hint("↑/↓", "move"),
			hint("/", "filter"),
			hint("enter", "run"),
			hint("q", "quit"),
		}, footerLabel.Render("   "))
	} else {
		hintRow = strings.Join([]string{
			hint("tab", "switch pane"),
			hint("↑/↓", "scroll"),
			hint("enter", "run"),
			hint("ctrl+c", cancelHint(m)),
		}, footerLabel.Render("   "))
	}

	left := m.renderRunStatus()
	gap := width - lipgloss.Width(left) - lipgloss.Width(hintRow)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hintRow

	return lipgloss.JoinVertical(lipgloss.Left,
		renderRule(width),
		bar,
	)
}

// cancelHint is the verb shown next to ctrl+c in the running footer.
func cancelHint(m model) string {
	if m.currentIndefinite != nil && m.currentIndefinite.cancelArmed {
		return "force kill"
	}
	if m.currentIndefinite != nil {
		return "stop"
	}
	return "quit"
}

// renderRunStatus is the left half of the footer bar. It reflects what's
// currently happening: idle, indefinite running with elapsed time, or
// quick jobs in flight.
func (m model) renderRunStatus() string {
	if !m.hasJobs() {
		return m.spinner.View() + footerLabel.Render(" ready")
	}
	if m.currentIndefinite == nil {
		// Quick jobs only.
		return m.spinner.View() + footerLabel.Render(" "+pluralizeJobs(len(m.jobs))+" running")
	}
	j := m.currentIndefinite
	main := m.spinner.View() + footerLabel.Render(" "+j.it.title+" · ") +
		stopwatchStyle.Render(formatElapsed(m.stopwatch.Elapsed()))
	if extras := len(m.jobs) - 1; extras > 0 {
		main += footerLabel.Render(" + " + pluralizeJobs(extras))
	}
	if j.cancelArmed {
		main += footerLabel.Render("   cancelling…")
	}
	return main
}

func pluralizeJobs(n int) string {
	if n == 1 {
		return "1 job"
	}
	return fmt.Sprintf("%d jobs", n)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// formatElapsed renders a duration as a compact, monospaced "M:SS.t" or
// "H:MM:SS" string. The 100ms stopwatch interval gives us tenths of seconds.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	tenths := int(d / (100 * time.Millisecond))
	totalSec := tenths / 10
	t := tenths % 10
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	s := totalSec % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d.%d", m, s, t)
}

func summaryToast(it item, err error, elapsed time.Duration) string {
	if err == nil {
		return statusOK.Render("✓") + " " +
			fmt.Sprintf("%s completed in %s", it.title, formatElapsed(elapsed))
	}
	return statusErr.Render("✗") + " " +
		fmt.Sprintf("%s failed (%s) after %s", it.title, errMessage(err), formatElapsed(elapsed))
}

func errMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 80 {
		return msg[:79] + "…"
	}
	return msg
}

// truncate clips s to a visible cell width of w, appending an ellipsis
// when it had to cut. Inputs are plain text (no ANSI), so a rune walk is
// fine.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if len(r) <= 1 {
		return string(r)
	}
	cut := w - 1
	if cut < 1 {
		cut = 1
	}
	if cut > len(r) {
		cut = len(r)
	}
	return string(r[:cut]) + "…"
}

func padOrTruncate(s string, w int) string {
	if lipgloss.Width(s) > w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Run starts the TLI to completion and returns the final model. The TLI
// dispatches selected commands itself, so callers don't need to fork a
// second subprocess afterwards.
func Run() (tea.Model, error) {
	return tea.NewProgram(newModel()).Run()
}

// RunAndExec drives the TLI; selected commands are executed in-loop, so
// by the time we return there's nothing further to do.
func RunAndExec() error {
	_, err := Run()
	return err
}
