// Package console runs a fixer-worker login or operator session.
// PTY bytes are appended raw to a log file the API tails over SSE; the
// browser terminal (xterm.js) replays them. Keystrokes arrive through
// the DB stdin queue so the UI never needs a websocket to the worker.
package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/fix/install"
	"github.com/alphabravocompany/thewolf/internal/models"
)

var urlRe = regexp.MustCompile(`https?://[^\s"'<>]+`)

// LoginArgs is the only command a login console may run.
func LoginArgs(engine string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "claude", "claude-code":
		return []string{"claude", "auth", "login"}, nil
	case "codex":
		return []string{"codex", "login"}, nil
	case "opencode":
		return []string{"opencode", "auth", "login"}, nil
	default:
		return nil, fmt.Errorf("unknown login engine %q", engine)
	}
}

// Run executes cons on this worker until the process exits or the context
// is cancelled. It updates the console row and writes the transcript.
func Run(ctx context.Context, store db.Store, fs *fixstore.Store, cons *models.FixerConsole) error {
	install.EnsureLocalBinOnPATH()
	if cons.Kind == models.FixerConsoleInstall {
		return runInstall(ctx, store, fs, cons)
	}
	args, err := commandFor(cons)
	if err != nil {
		return err
	}
	if CommandForTest == nil && cons.Kind != models.FixerConsoleShell {
		if _, lookErr := exec.LookPath(args[0]); lookErr != nil {
			msg := fmt.Sprintf("%s is not installed on the fixer worker. Click Install %s, then try login again.", args[0], displayName(args[0]))
			return failConsole(ctx, store, fs, cons, msg)
		}
	}
	if CommandForTest == nil {
		args = maybePTY(args)
	}
	now := time.Now().UTC()
	cons.Status = models.FixerConsoleRunning
	cons.StartedAt = &now
	if err := store.UpdateFixerConsole(ctx, cons); err != nil {
		return err
	}

	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec G204 -- args are a fixed login/shell allowlist
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"COLUMNS=120",
		"LINES=36",
	)
	if home != "" {
		cmd.Dir = home
		cmd.Env = append(cmd.Env, "HOME="+home)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if fs != nil {
		_ = fs.AppendConsole(cons.ID, "$ "+strings.Join(commandForOrArgs(cons, args), " "))
	}

	if err := cmd.Start(); err != nil {
		if isMissingExec(err) {
			msg := fmt.Sprintf("%s is not installed on the fixer worker. Click Install %s, then try login again.", args[0], displayName(args[0]))
			return failConsole(ctx, store, fs, cons, msg)
		}
		return err
	}

	pumpCtx, stopPump := context.WithCancel(ctx)
	defer stopPump()

	var wg sync.WaitGroup
	wg.Add(1)
	claim := *cons
	go func() {
		defer wg.Done()
		pumpStdin(pumpCtx, store, &claim, stdin)
	}()

	// Drain stdout before Wait: Wait closes StdoutPipe and drops unread bytes
	// (os/exec: "incorrect to call Wait before all reads from the pipe have
	// completed"). Fast commands + -race on Ubuntu hit that every time.
	relayOutput(ctx, store, fs, cons, stdout)
	stopPump()
	_ = stdin.Close()
	wg.Wait()
	waitErr := cmd.Wait()

	finish := time.Now().UTC()
	cons.Status = models.FixerConsoleExited
	cons.FinishedAt = &finish
	if waitErr != nil && ctx.Err() == nil {
		cons.Error = waitErr.Error()
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return store.UpdateFixerConsole(writeCtx, cons)
}

// CommandForTest, when set, replaces commandFor. Tests use it to run
// /bin/echo instead of a login CLI or interactive shell.
var CommandForTest func(*models.FixerConsole) ([]string, error)

func commandFor(cons *models.FixerConsole) ([]string, error) {
	if CommandForTest != nil {
		return CommandForTest(cons)
	}
	switch cons.Kind {
	case models.FixerConsoleShell:
		return []string{"/bin/sh"}, nil
	default:
		return LoginArgs(cons.Engine)
	}
}

func commandForOrArgs(cons *models.FixerConsole, wrapped []string) []string {
	if args, err := commandFor(cons); err == nil {
		return args
	}
	return wrapped
}

// maybePTY wraps a login CLI in `script` when available so tools that
// insist on a TTY still print their OAuth URL. Operator shells stay on
// a plain pipe so stdin from the UI queue is delivered reliably.
func maybePTY(args []string) []string {
	if len(args) == 0 {
		return args
	}
	switch args[0] {
	case "claude", "codex", "opencode":
	default:
		return args
	}
	if _, err := exec.LookPath("script"); err != nil {
		return args
	}
	return []string{"script", "-qefc", "stty cols 120 rows 36; exec " + strings.Join(args, " "), "/dev/null"}
}

func relayOutput(ctx context.Context, store db.Store, fs *fixstore.Store, cons *models.FixerConsole, r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if fs != nil {
				_ = fs.AppendConsoleRaw(cons.ID, chunk)
			}
			rememberURL(ctx, store, cons, string(chunk))
		}
		if err != nil {
			return
		}
	}
}

func rememberURL(ctx context.Context, store db.Store, cons *models.FixerConsole, text string) {
	u := firstURL(text)
	if u == "" || cons.LastURL == u {
		return
	}
	cons.LastURL = u
	_ = store.UpdateFixerConsole(ctx, cons)
}

func pumpStdin(ctx context.Context, store db.Store, cons *models.FixerConsole, w io.WriteCloser) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			chunks, err := store.DrainFixerConsoleStdin(ctx, cons)
			if err != nil {
				return
			}
			for _, c := range chunks {
				if _, err := io.WriteString(w, c); err != nil {
					return
				}
			}
		}
	}
}

func firstURL(line string) string {
	return urlRe.FindString(line)
}

func runInstall(ctx context.Context, store db.Store, fs *fixstore.Store, cons *models.FixerConsole) error {
	now := time.Now().UTC()
	cons.Status = models.FixerConsoleRunning
	cons.StartedAt = &now
	if err := store.UpdateFixerConsole(ctx, cons); err != nil {
		return err
	}
	logf := func(line string) {
		if fs != nil {
			_ = fs.AppendConsole(cons.ID, line)
		}
	}
	logf("$ wolf fixer install " + cons.Engine)
	err := install.Install(ctx, cons.Engine, logf)
	finish := time.Now().UTC()
	cons.Status = models.FixerConsoleExited
	cons.FinishedAt = &finish
	if err != nil {
		cons.Error = err.Error()
		logf("install failed: " + err.Error())
	} else {
		logf("ready — you can log in now")
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return store.UpdateFixerConsole(writeCtx, cons)
}

func failConsole(ctx context.Context, store db.Store, fs *fixstore.Store, cons *models.FixerConsole, msg string) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if fs != nil {
		_ = fs.AppendConsole(cons.ID, msg)
	}
	now := time.Now().UTC()
	if cons.StartedAt == nil {
		cons.StartedAt = &now
	}
	cons.Status = models.FixerConsoleExited
	cons.FinishedAt = &now
	cons.Error = msg
	return errors.Join(errors.New(msg), store.UpdateFixerConsole(writeCtx, cons))
}

func displayName(command string) string {
	switch command {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	default:
		return command
	}
}

func isMissingExec(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "executable file not found") || strings.Contains(s, "not found in $PATH")
}
