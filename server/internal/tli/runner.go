package tli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	goruntime "runtime"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// tailMaxLines caps how many lines of merged stdout/stderr we hold for the
// runner view. Older lines scroll off the top.
const tailMaxLines = 200

// runOutputMsg delivers a batch of merged stdout/stderr lines from the
// running subprocess to the bubbletea Update loop.
type runOutputMsg struct {
	lines []string
}

// runDoneMsg fires once when the subprocess exits.
type runDoneMsg struct {
	err     error
	elapsed time.Duration
}

// runStartedMsg confirms the runner has been spawned. Used so Update can
// kick off the first poll cmd before any output arrives.
type runStartedMsg struct{}

// runStartFailedMsg surfaces a failure to even spawn the subprocess.
type runStartFailedMsg struct {
	err error
}

// runner owns a forked subprocess plus a tailing buffer of its merged
// stdout + stderr. It's the model's bridge between the OS-level command
// and the bubbletea event loop.
type runner struct {
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	started time.Time

	out    chan string
	doneCh chan error

	mu   sync.Mutex
	tail []string
}

// startRunner forks name+args with captured pipes, returning the runner
// and a tea.Cmd that delivers the first batch of output (or runDoneMsg if
// the subprocess never wrote anything before exiting).
//
// Cancellation: r.Cancel() sends SIGINT on Unix and Kill() on Windows
// (the only supported signal). cmd.WaitDelay is set to 5s so a stuck
// subprocess gets force-killed after the grace window.
func startRunner(name string, args []string) (*runner, tea.Cmd, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, name, args...)

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if goruntime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("start %s: %w", name, err)
	}

	out := make(chan string, 256)
	doneCh := make(chan error, 1)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scanInto(stdout, out) }()
	go func() { defer wg.Done(); scanInto(stderr, out) }()
	go func() {
		wg.Wait()
		close(out)
		doneCh <- cmd.Wait()
		close(doneCh)
	}()

	r := &runner{
		cmd:     cmd,
		cancel:  cancel,
		started: time.Now(),
		out:     out,
		doneCh:  doneCh,
	}
	return r, r.poll(), nil
}

// scanInto reads lines from r and forwards them to out. It exits cleanly
// on EOF or any read error so the wait-group can release.
func scanInto(r io.Reader, out chan<- string) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		// Non-blocking-ish send: if the channel buffer is full we drop
		// the line rather than freeze the subprocess. Output that fast
		// would overwhelm the TUI anyway.
		select {
		case out <- s.Text():
		default:
		}
	}
}

// poll returns a tea.Cmd that reads one line, drains whatever else is
// immediately available (up to a small batch), and emits it as a
// runOutputMsg. When the channel is closed it emits runDoneMsg with the
// final exit error and elapsed time.
func (r *runner) poll() tea.Cmd {
	return func() tea.Msg {
		line, ok := <-r.out
		if !ok {
			err := <-r.doneCh
			return runDoneMsg{err: err, elapsed: time.Since(r.started)}
		}
		batch := []string{line}
	drain:
		for len(batch) < 32 {
			select {
			case more, ok := <-r.out:
				if !ok {
					break drain
				}
				batch = append(batch, more)
			default:
				break drain
			}
		}
		for _, l := range batch {
			r.appendTail(l)
		}
		return runOutputMsg{lines: batch}
	}
}

// appendTail records line in the rolling tail buffer, evicting from the
// front once we exceed tailMaxLines.
func (r *runner) appendTail(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tail = append(r.tail, line)
	if len(r.tail) > tailMaxLines {
		r.tail = r.tail[len(r.tail)-tailMaxLines:]
	}
}

// Tail returns a snapshot of the most recent up-to-tailMaxLines lines.
func (r *runner) Tail() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.tail))
	copy(out, r.tail)
	return out
}

// Cancel asks the subprocess to terminate. On Unix this is SIGINT (so the
// child can shut down gracefully); on Windows it's a hard Kill.
func (r *runner) Cancel() {
	if r == nil {
		return
	}
	r.cancel()
}

// Started reports when the subprocess began running. The model's stopwatch
// is the user-visible timer; this is just for tests / diagnostics.
func (r *runner) Started() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.started
}
