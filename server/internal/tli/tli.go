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
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type item struct {
	title string
	desc  string
	cmd   []string
	badge string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title + " " + i.desc + " " + strings.Join(i.cmd, " ") }

type model struct {
	list     list.Model
	spinner  spinner.Model
	viewport viewport.Model
	status   string
	run      bool
	ready    bool
	width    int
	height   int
}

var (
	pink      = lipgloss.Color("205")
	rose      = lipgloss.Color("197")
	orange    = lipgloss.Color("208")
	yellow    = lipgloss.Color("226")
	green     = lipgloss.Color("46")
	cyan      = lipgloss.Color("51")
	blue      = lipgloss.Color("57")
	purple    = lipgloss.Color("201")
	muted     = lipgloss.Color("244")
	panelBg   = lipgloss.Color("235")
	panelLine = lipgloss.Color("62")

	logoStyle  = lipgloss.NewStyle().Foreground(pink).Bold(true)
	titleStyle = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	mutedStyle = lipgloss.NewStyle().Foreground(muted)

	appStyle  = lipgloss.NewStyle().Padding(1, 2)
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(panelLine).
			Background(panelBg).
			Padding(1, 2)
	footerStyle    = lipgloss.NewStyle().Foreground(muted).PaddingTop(1)
	pillStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(panelLine).Padding(0, 1).Bold(true)
	quickPillStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(yellow).Padding(0, 1).Bold(true)
)

type delegate struct{}

func (d delegate) Height() int                             { return 3 }
func (d delegate) Spacing() int                            { return 1 }
func (d delegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d delegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}
	selected := index == m.Index()
	title := it.title
	if it.title == "Design" {
		title = rainbow("✦ DESIGN ✦")
	}
	badge := it.badge
	if badge == "" {
		badge = strings.Join(it.cmd, " ")
	}
	badgeStyle := pillStyle
	if it.title == "Design" {
		badgeStyle = quickPillStyle
	}
	rowStyle := lipgloss.NewStyle().Padding(0, 1)
	if selected {
		rowStyle = rowStyle.Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(pink).Background(lipgloss.Color("236"))
	}
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(cyan)
	if selected {
		nameStyle = nameStyle.Foreground(pink)
	}
	if it.title == "Design" {
		nameStyle = nameStyle.Foreground(yellow)
	}
	line1 := lipgloss.JoinHorizontal(lipgloss.Center, nameStyle.Render(title), " ", badgeStyle.Render(badge))
	line2 := mutedStyle.Render(it.desc)
	line3 := mutedStyle.Render("  " + strings.Join(it.cmd, " "))
	fmt.Fprint(w, rowStyle.Width(max(20, m.Width()-4)).Render(line1+"\n"+line2+"\n"+line3))
}

func Run() error {
	_, err := tea.NewProgram(newModel()).Run()
	return err
}

func newModel() model {
	items := defaultItems()
	li := make([]list.Item, len(items))
	for i := range items {
		li[i] = items[i]
	}
	l := list.New(li, delegate{}, 78, 24)
	l.Title = "Choose your next step"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)
	l.Styles.Title = lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(cyan).Padding(0, 1).Bold(true)
	l.Styles.PaginationStyle = mutedStyle
	l.Styles.NoItems = mutedStyle
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = lipgloss.NewStyle().Foreground(pink)
	vp := viewport.New(78, 8)
	vp.SetContent(logoStyle.Render(branding.Logo))
	return model{list: l, spinner: s, viewport: vp, status: "enter run • / filter • ↑/↓ move • q quit"}
}

func defaultItems() []item {
	return []item{
		{"Install", "Install/check Docker, Go, and Node; tries platform package managers before links.", []string{"boxland", "install"}, "setup"},
		{"Design", "QUICK START: dependencies, migrations, web build, staging, then serve Boxland.", []string{"boxland", "design"}, "quick start"},
		{"Serve", "Run the Go server only.", []string{"boxland", "serve"}, "server"},
		{"Up", "Start Postgres, Redis, Mailpit, and MinIO with Docker Compose.", []string{"boxland", "up"}, "docker"},
		{"Down", "Stop Docker dependencies.", []string{"boxland", "down"}, "docker"},
		{"Migrate", "Apply pending SQL migrations.", []string{"boxland", "migrate", "up"}, "database"},
		{"Backup", "Export a complete restore bundle into ./backups.", []string{"boxland", "backup", "export", defaultBackupPath()}, "safety"},
		{"Restore", "Restore from ./backups/latest.tar.gz. Destructive; CLI asks you to pass --yes.", []string{"boxland", "backup", "import", filepath.Join("backups", "latest.tar.gz")}, "restore"},
		{"Test", "Run Go, web, scripts, and realm isolation tests.", []string{"boxland", "test"}, "quality"},
	}
}

func defaultBackupPath() string {
	return filepath.Join("backups", "boxland-"+time.Now().Format("20060102-150405")+".tar.gz")
}

func (m model) Init() tea.Cmd { return tea.Batch(m.spinner.Tick) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		contentWidth := max(64, msg.Width-6)
		m.list.SetSize(contentWidth, max(12, msg.Height-16))
		m.viewport.Width = contentWidth
		m.viewport.Height = 8
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c", "q", "esc"))):
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			m.run = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	cmds = append(cmds, cmd)
	m.spinner, cmd = m.spinner.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if !m.ready {
		return "\n  " + m.spinner.View() + " Loading Boxland..."
	}
	header := lipgloss.JoinVertical(lipgloss.Left,
		m.viewport.View(),
		lipgloss.JoinHorizontal(lipgloss.Center,
			titleStyle.Render("Boxland TLI"),
			"  ",
			quickPillStyle.Render("Start with Design"),
			"  ",
			mutedStyle.Render("everything runs through `boxland`"),
		),
	)
	body := cardStyle.Width(max(64, m.width-8)).Render(m.list.View())
	footer := footerStyle.Render(m.spinner.View() + " " + m.status)
	return appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, "", body, footer))
}

func renderItemTitle(title string, selected bool) string {
	if title == "Design" {
		label := rainbow("✦ DESIGN ✦")
		if selected {
			return lipgloss.NewStyle().Bold(true).Render(label)
		}
		return label
	}
	if selected {
		return lipgloss.NewStyle().Foreground(pink).Bold(true).Render(title)
	}
	return title
}

func rainbow(s string) string {
	colors := []lipgloss.Color{"196", "208", "226", "46", "51", "57", "201"}
	var b strings.Builder
	for i, r := range s {
		if r == ' ' {
			b.WriteRune(r)
			continue
		}
		b.WriteString(lipgloss.NewStyle().Foreground(colors[i%len(colors)]).Bold(true).Render(string(r)))
	}
	return b.String()
}

func (m model) selected() (item, bool) {
	if !m.run {
		return item{}, false
	}
	it, ok := m.list.SelectedItem().(item)
	if !ok {
		return item{}, false
	}
	return it, true
}

func ExecSelected(m tea.Model) error {
	mm, ok := m.(model)
	if !ok {
		return nil
	}
	it, ok := mm.selected()
	if !ok {
		return nil
	}
	fmt.Printf("\n› %s\n\n", strings.Join(it.cmd, " "))
	cmd := exec.Command(it.cmd[0], it.cmd[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func RunAndExec() error {
	final, err := tea.NewProgram(newModel()).Run()
	if err != nil {
		return err
	}
	return ExecSelected(final)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
