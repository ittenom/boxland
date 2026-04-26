// Package tli renders the Boxland Terminal Launch Interface — the menu you
// see when you run `boxland` with no arguments.
//
// The TLI is built on Charmbracelet's bubbles + lipgloss components:
//
//   - viewport.Model holds the gradient logo header so it stays anchored at
//     the top and gracefully overflows on tiny terminals.
//   - list.Model owns the menu items, with a custom ItemDelegate that
//     renders each row in clean tabular form (no background pills, color
//     applied to text, ▎ as the selection bar).
//   - spinner.Model ticks in the footer to show the program is alive.
//   - stopwatch.Model times the running subcommand (install, design, …).
//   - list.NewStatusMessage flashes a "✓ done in 1m 23s" toast in the list
//     status bar after a run returns.
//   - viewport.Model (a second one) tails captured stdout/stderr while a
//     non-interactive command is running.
//
// Style cues come from the lipgloss "layout" example: thin underline rules,
// columns aligned without dividers, and color carried by foreground only.
package tli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"boxland/server/internal/branding"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/stopwatch"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// phase tracks which view the model is currently rendering.
type phase int

const (
	phaseMenu phase = iota
	phaseRun
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

// model wires the bubbles components together.
type model struct {
	phase phase

	list      list.Model
	spinner   spinner.Model
	header    viewport.Model
	tail      viewport.Model
	stopwatch stopwatch.Model

	width  int
	height int
	ready  bool

	// Run state.
	running     item       // item currently executing (valid in phaseRun)
	runner      *runner    // captured-pipe runner (nil for interactive items)
	runErr      error      // last run's exit error
	runDone     bool       // subprocess has exited
	runElapsed  time.Duration
	cancelArmed bool // user pressed cancel once; another press force-kills

	// run is set true on successful exit from phaseMenu so RunAndExec knows
	// not to re-shell out (we already ran the command in-loop).
	exitChosen bool
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

	// Per-item field column widths.
	nameWidth  = 10
	badgeWidth = 13

	nameUnsel   = lipgloss.NewStyle().Width(nameWidth).Bold(true).Foreground(cCyan)
	nameSel     = lipgloss.NewStyle().Width(nameWidth).Bold(true).Foreground(cPink)
	nameFeat    = lipgloss.NewStyle().Width(nameWidth).Bold(true).Foreground(cYellow)
	nameFeatSel = lipgloss.NewStyle().Width(nameWidth).Bold(true).Foreground(cYellow).Underline(true)

	badgeStyle     = lipgloss.NewStyle().Width(badgeWidth).Foreground(cPurple)
	badgeFeatStyle = lipgloss.NewStyle().Width(badgeWidth).Foreground(cYellow).Bold(true)

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

	docStyle = lipgloss.NewStyle().Padding(1, 2)
)

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

	bstyle := badgeStyle
	if it.featured {
		bstyle = badgeFeatStyle
	}
	badge := bstyle.Render(padOrTruncate(it.badge, badgeWidth))

	gap := " "
	used := lipgloss.Width(gutter) + lipgloss.Width(name) + lipgloss.Width(badge) + lipgloss.Width(gap)
	descWidth := m.Width() - used
	if descWidth < 20 {
		descWidth = 20
	}
	desc := descStyle.Render(truncate(it.desc, descWidth))

	headerRow := gutter + name + badge + gap + desc
	indent := strings.Repeat(" ", used)
	cmdRow := indent + cmdStyle.Render("$ "+strings.Join(it.cmd, " "))

	fmt.Fprint(w, headerRow+"\n"+cmdRow)
}

func defaultItems() []item {
	return []item{
		{title: "Install", badge: "setup", desc: "Install/check Docker, Go, and Node; tries platform package managers before links.", cmd: []string{"boxland", "install"}, interactive: true},
		{title: "Design", badge: "quick start", desc: "Dependencies, migrations, web build, staging, then serve Boxland.", cmd: []string{"boxland", "design"}, featured: true, indefinite: true},
		{title: "Serve", badge: "server", desc: "Run the Go server only.", cmd: []string{"boxland", "serve"}, indefinite: true},
		{title: "Up", badge: "docker", desc: "Start Postgres, Redis, Mailpit, and MinIO with Docker Compose.", cmd: []string{"boxland", "up"}},
		{title: "Down", badge: "docker", desc: "Stop Docker dependencies.", cmd: []string{"boxland", "down"}},
		{title: "Migrate", badge: "database", desc: "Apply pending SQL migrations.", cmd: []string{"boxland", "migrate", "up"}},
		{title: "Backup", badge: "safety", desc: "Export a complete restore bundle into ./backups.", cmd: []string{"boxland", "backup", "export", defaultBackupPath()}},
		{title: "Restore", badge: "restore", desc: "Restore from ./backups/latest.tar.gz. Destructive; CLI asks you to pass --yes.", cmd: []string{"boxland", "backup", "import", filepath.Join("backups", "latest.tar.gz")}, interactive: true},
		{title: "Test", badge: "quality", desc: "Run Go, web, scripts, and realm isolation tests.", cmd: []string{"boxland", "test"}},
	}
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

	return model{
		phase:     phaseMenu,
		list:      l,
		spinner:   s,
		header:    header,
		tail:      tail,
		stopwatch: sw,
	}
}

// headerHeight is the number of lines the logo + tagline + rule occupies.
func headerHeight() int {
	logoLines := strings.Count(strings.TrimRight(branding.Logo, "\n"), "\n") + 1
	// logo + blank + tagline + rule
	return logoLines + 3
}

func (m model) Init() tea.Cmd { return m.spinner.Tick }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseMenu:
		return m.updateMenu(msg)
	case phaseRun:
		return m.updateRun(msg)
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Menu phase
// ---------------------------------------------------------------------------

func (m model) updateMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.applySize(msg.Width, msg.Height)

	case runDoneMsg:
		// Returned from an interactive (tea.ExecProcess) run.
		return m.handleRunDone(msg)

	case tea.KeyMsg:
		// Don't intercept keys while the list's filter input is active.
		if m.list.FilterState() != list.Filtering {
			switch {
			case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
				return m, tea.Quit
			case key.Matches(msg, key.NewBinding(key.WithKeys("q", "esc"))):
				if m.list.FilterState() == list.Unfiltered {
					return m, tea.Quit
				}
			case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
				return m.startSelected()
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	cmds = append(cmds, cmd)

	m.spinner, cmd = m.spinner.Update(msg)
	cmds = append(cmds, cmd)

	m.header, cmd = m.header.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// startSelected transitions to phaseRun and either spawns a captured-pipe
// runner or delegates to tea.ExecProcess for items that need a real TTY.
func (m model) startSelected() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok || len(it.cmd) == 0 {
		return m, nil
	}

	bin, args := resolveCmd(it.cmd)

	m.phase = phaseRun
	m.running = it
	m.runner = nil
	m.runErr = nil
	m.runDone = false
	m.runElapsed = 0
	m.cancelArmed = false
	m.tail.GotoTop()
	m.tail.SetContent("")

	// Reset + start the stopwatch.
	resetCmd := m.stopwatch.Reset()
	startCmd := m.stopwatch.Start()

	if it.interactive {
		// Hand the terminal over directly; bubbletea suspends the TUI for
		// the duration of the subprocess and resumes after.
		started := time.Now()
		c := exec.Command(bin, args...)
		execCmd := tea.ExecProcess(c, func(err error) tea.Msg {
			return runDoneMsg{err: err, elapsed: time.Since(started)}
		})
		return m, tea.Batch(resetCmd, startCmd, execCmd)
	}

	// Non-interactive: capture pipes and stream into the runner view.
	r, pollCmd, err := startRunner(bin, args)
	if err != nil {
		return m, func() tea.Msg { return runStartFailedMsg{err: err} }
	}
	m.runner = r
	return m, tea.Batch(resetCmd, startCmd, pollCmd)
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
// Run phase
// ---------------------------------------------------------------------------

func (m model) updateRun(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.applySize(msg.Width, msg.Height)

	case runOutputMsg:
		m.appendTail(msg.lines)
		if m.runner != nil {
			cmds = append(cmds, m.runner.poll())
		}

	case runDoneMsg:
		return m.handleRunDone(msg)

	case runStartFailedMsg:
		return m.handleRunDone(runDoneMsg{err: msg.err})

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
			return m.handleCancel()
		case m.runDone && key.Matches(msg, key.NewBinding(key.WithKeys("enter", " "))):
			return m.returnToMenu()
		case m.runDone && key.Matches(msg, key.NewBinding(key.WithKeys("q", "esc"))):
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("q"))):
			// While running, q forwards a graceful cancel.
			return m.handleCancel()
		}
	}

	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	cmds = append(cmds, cmd)

	m.stopwatch, cmd = m.stopwatch.Update(msg)
	cmds = append(cmds, cmd)

	m.tail, cmd = m.tail.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m model) handleCancel() (tea.Model, tea.Cmd) {
	if m.runDone {
		// Already finished — just go back to the menu.
		return m.returnToMenu()
	}
	if m.runner == nil {
		// Interactive run; tea.ExecProcess owns the terminal already.
		return m, nil
	}
	m.runner.Cancel()
	m.cancelArmed = true
	return m, nil
}

func (m model) handleRunDone(msg runDoneMsg) (tea.Model, tea.Cmd) {
	m.runDone = true
	m.runErr = msg.err
	m.runElapsed = msg.elapsed
	stopCmd := m.stopwatch.Stop()
	// For indefinite items the user explicitly cancelled, so an error is
	// expected and we shouldn't shout about it.
	if m.running.indefinite && m.cancelArmed {
		m.runErr = nil
	}
	return m, stopCmd
}

func (m model) returnToMenu() (tea.Model, tea.Cmd) {
	toast := summaryToast(m.running, m.runErr, m.runElapsed)
	statusCmd := m.list.NewStatusMessage(toast)

	m.phase = phaseMenu
	m.runner = nil
	m.runDone = false
	m.cancelArmed = false
	m.tail.SetContent("")
	resetCmd := m.stopwatch.Reset()

	return m, tea.Batch(resetCmd, statusCmd)
}

// appendTail rebuilds the tail viewport from the runner's rolling buffer.
// We rebuild rather than appending because the buffer drops oldest lines
// once it exceeds tailMaxLines, and a viewport doesn't have a "drop top"
// primitive of its own.
func (m *model) appendTail(lines []string) {
	if len(lines) == 0 || m.runner == nil {
		return
	}
	m.tail.SetContent(tailStyle.Render(strings.Join(m.runner.Tail(), "\n")))
	m.tail.GotoBottom()
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

	listHeight := h - headerHeight() - 5
	if listHeight < 8 {
		listHeight = 8
	}
	m.list.SetSize(contentWidth, listHeight)

	tailHeight := h - headerHeight() - 8
	if tailHeight < 6 {
		tailHeight = 6
	}
	m.tail.Width = contentWidth
	m.tail.Height = tailHeight
}

// ---------------------------------------------------------------------------
// Views
// ---------------------------------------------------------------------------

func (m model) View() string {
	if !m.ready {
		return "\n  " + m.spinner.View() + " Loading Boxland…\n"
	}

	switch m.phase {
	case phaseRun:
		return m.viewRun()
	default:
		return m.viewMenu()
	}
}

func (m model) viewMenu() string {
	footer := m.renderMenuFooter(m.header.Width)
	return docStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		m.header.View(),
		m.list.View(),
		footer,
	))
}

func (m model) viewRun() string {
	width := m.header.Width
	it := m.running

	// Run header: name + badge on the left, stopwatch on the right.
	nameStyleRun := nameUnsel
	if it.featured {
		nameStyleRun = nameFeat
	}
	bstyle := badgeStyle
	if it.featured {
		bstyle = badgeFeatStyle
	}
	left := chevSel.Render("◆ Running ") + nameStyleRun.Render(it.title) + bstyle.Render(it.badge)
	right := stopwatchStyle.Render(formatElapsed(m.stopwatch.Elapsed()))
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	headRow := left + strings.Repeat(" ", gap) + right

	cmdRow := cmdStyle.Render("$ " + strings.Join(it.cmd, " "))

	body := m.tail.View()
	if strings.TrimSpace(body) == "" {
		body = footerLabel.Render("  (waiting for output…)")
	}

	footer := m.renderRunFooter(width)

	return docStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		m.header.View(),
		headRow,
		cmdRow,
		renderRule(width),
		body,
		footer,
	))
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

func (m model) renderMenuFooter(width int) string {
	hint := func(k, label string) string {
		return footerKey.Render(k) + footerLabel.Render(" "+label)
	}
	hints := strings.Join([]string{
		hint("↑/↓", "move"),
		hint("/", "filter"),
		hint("enter", "run"),
		hint("q", "quit"),
	}, footerLabel.Render("   "))

	left := m.spinner.View() + footerLabel.Render(" ready")
	gap := width - lipgloss.Width(left) - lipgloss.Width(hints)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hints

	return lipgloss.JoinVertical(lipgloss.Left,
		renderRule(width),
		bar,
	)
}

func (m model) renderRunFooter(width int) string {
	var statusLeft string
	switch {
	case m.runDone && m.runErr == nil:
		statusLeft = statusOK.Render("✓ done in "+formatElapsed(m.runElapsed)) +
			footerLabel.Render("   enter return to menu · q quit")
	case m.runDone:
		statusLeft = statusErr.Render("✗ "+errMessage(m.runErr)) +
			footerLabel.Render(" after "+formatElapsed(m.runElapsed)+
				"   enter return to menu · q quit")
	case m.cancelArmed:
		statusLeft = m.spinner.View() +
			footerLabel.Render(" cancelling · ctrl+c again to force kill")
	case m.running.indefinite:
		statusLeft = m.spinner.View() +
			footerLabel.Render(" running for "+formatElapsed(m.stopwatch.Elapsed())+
				"   ctrl+c stop · q stop")
	default:
		statusLeft = m.spinner.View() +
			footerLabel.Render(" running…   ctrl+c cancel")
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		renderRule(width),
		statusLeft,
	)
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
		return fmt.Sprintf("✓ %s completed in %s", it.title, formatElapsed(elapsed))
	}
	return fmt.Sprintf("✗ %s failed (%s) after %s", it.title, errMessage(err), formatElapsed(elapsed))
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

// truncate clips s to a visible cell width of w, appending an ellipsis when
// it had to cut. Inputs are plain text (no ANSI), so a rune walk is fine.
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

// RunAndExec drives the TLI; selected commands are executed in-loop, so by
// the time we return there's nothing further to do.
func RunAndExec() error {
	_, err := Run()
	return err
}
