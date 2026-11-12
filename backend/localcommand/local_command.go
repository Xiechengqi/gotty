package localcommand

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

const (
	DefaultCloseSignal  = syscall.SIGINT
	DefaultCloseTimeout = 10 * time.Second
)

type LocalCommand struct {
	command string
	argv    []string

	closeSignal  syscall.Signal
	closeTimeout time.Duration

	cmd       *exec.Cmd
	pty       *os.File
	ptyClosed chan struct{}
	writeGate chan struct{}
}

func New(command string, argv []string, headers map[string][]string, options ...Option) (*LocalCommand, error) {
	cmd := exec.Command(command, argv...)

	// Filter out tmux-related env vars leaked from gotty's parent process.
	// They mislead clients like Claude Code into thinking they run inside a
	// tmux session, which breaks clipboard integration and /terminal-setup.
	filtered := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			continue
		}
		filtered = append(filtered, e)
	}
	cmd.Env = append(filtered, "TERM=xterm-256color")

	// Combine headers into key=value pairs to set as env vars
	// Prefix the headers with "http_" so we don't overwrite any other env vars
	// which potentially has the same name and to bring these closer to what
	// a (F)CGI server would proxy to a backend service
	// Replace hyphen with underscore and make them all upper case
	for key, values := range headers {
		h := "HTTP_" + strings.Replace(strings.ToUpper(key), "-", "_", -1) + "=" + strings.Join(values, ",")
		// log.Printf("Adding header: %s", h)
		cmd.Env = append(cmd.Env, h)
	}

	pty, err := pty.Start(cmd)
	if err != nil {
		// todo close cmd?
		return nil, errors.Wrapf(err, "failed to start command `%s`", command)
	}
	pty, err = pollablePTY(pty)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	ptyClosed := make(chan struct{})

	lcmd := &LocalCommand{
		command: command,
		argv:    argv,

		closeSignal:  DefaultCloseSignal,
		closeTimeout: DefaultCloseTimeout,

		cmd:       cmd,
		pty:       pty,
		ptyClosed: ptyClosed,
		writeGate: make(chan struct{}, 1),
	}

	for _, option := range options {
		option(lcmd)
	}

	// When the process is closed by the user,
	// close pty so that Read() on the pty breaks with an EOF.
	go func() {
		defer func() {
			lcmd.pty.Close()
			close(lcmd.ptyClosed)
		}()

		lcmd.cmd.Wait()
	}()

	return lcmd, nil
}

func (lcmd *LocalCommand) Read(p []byte) (n int, err error) {
	return lcmd.pty.Read(p)
}

func (lcmd *LocalCommand) Write(p []byte) (n int, err error) {
	return lcmd.WriteContext(context.Background(), p)
}

func (lcmd *LocalCommand) Close() error {
	if lcmd.cmd != nil && lcmd.cmd.Process != nil {
		lcmd.cmd.Process.Signal(lcmd.closeSignal)
	}
	for {
		select {
		case <-lcmd.ptyClosed:
			return nil
		case <-lcmd.closeTimeoutC():
			lcmd.cmd.Process.Signal(syscall.SIGKILL)
		}
	}
}

func (lcmd *LocalCommand) WindowTitleVariables() map[string]interface{} {
	return map[string]interface{}{
		"command": lcmd.command,
		"argv":    lcmd.argv,
		"pid":     lcmd.cmd.Process.Pid,
	}
}

// GetWorkingDir returns the current working directory of the process.
// On Linux, this reads from /proc/<pid>/cwd.
func (lcmd *LocalCommand) GetWorkingDir() (string, error) {
	if lcmd.cmd != nil && lcmd.cmd.Process != nil {
		return os.Readlink("/proc/" + strconv.Itoa(lcmd.cmd.Process.Pid) + "/cwd")
	}
	return "", errors.New("process not found")
}

func (lcmd *LocalCommand) ResizeTerminal(width int, height int) error {
	window := unix.Winsize{
		Row: uint16(height),
		Col: uint16(width),
	}
	// File.Fd() would switch our pollable PTY back to blocking I/O.
	raw, err := lcmd.pty.SyscallConn()
	if err != nil {
		return err
	}
	var resizeErr error
	err = raw.Control(func(fd uintptr) { resizeErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &window) })
	if err != nil {
		return err
	}
	return resizeErr
}

func (lcmd *LocalCommand) closeTimeoutC() <-chan time.Time {
	if lcmd.closeTimeout >= 0 {
		return time.After(lcmd.closeTimeout)
	}

	return make(chan time.Time)
}
