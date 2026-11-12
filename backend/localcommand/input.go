package localcommand

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// Register a nonblocking PTY with Go's poller, so a program which stops reading
// cannot make a paste writer (or cancellation) hang forever. Fd() can put the
// original os.File into blocking mode; the new wrapper detects O_NONBLOCK.
func pollablePTY(original *os.File) (*os.File, error) {
	defer original.Close()
	syscall.ForkLock.RLock()
	fd, err := syscall.Dup(int(original.Fd()))
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	if err = syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), original.Name())
	if err = file.SetWriteDeadline(time.Time{}); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// All PTY writers share one gate. Deadlines affect writes only, never output
// reads. Check cancellation at most 100 ms after a blocked kernel write.
func (lcmd *LocalCommand) WriteContext(ctx context.Context, p []byte) (int, error) {
	select {
	case lcmd.writeGate <- struct{}{}:
		defer func() { <-lcmd.writeGate }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	defer lcmd.pty.SetWriteDeadline(time.Time{})
	total := 0
	for total < len(p) {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if err := lcmd.pty.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			return total, err
		}
		n, err := lcmd.pty.Write(p[total:])
		total += n
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			return total, err
		}
	}
	return total, nil
}
