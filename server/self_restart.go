package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const selfRestartScript = `parent=$1
shift
i=0
while kill -0 "$parent" 2>/dev/null && [ "$i" -lt 100 ]; do
	sleep 0.1
	i=$((i + 1))
done
if kill -0 "$parent" 2>/dev/null; then
	kill -TERM "$parent" 2>/dev/null || true
	i=0
	while kill -0 "$parent" 2>/dev/null && [ "$i" -lt 50 ]; do
		sleep 0.1
		i=$((i + 1))
	done
fi
if kill -0 "$parent" 2>/dev/null; then
	kill -KILL "$parent" 2>/dev/null || true
	sleep 0.2
fi
exec "$0" "$@"`

func scheduleSelfRestart() error {
	binary, err := currentExecutablePath()
	if err != nil {
		return err
	}

	cmd := exec.Command("sh", "-c", selfRestartScript, binary, strconv.Itoa(os.Getpid()))
	cmd.Args = append(cmd.Args, os.Args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func currentExecutablePath() (string, error) {
	var candidates []string
	if exe, err := os.Executable(); err == nil && exe != "" {
		candidates = append(candidates, exe)
		if stripped := strings.TrimSuffix(exe, " (deleted)"); stripped != exe {
			candidates = append(candidates, stripped)
		}
	}

	if len(os.Args) > 0 && os.Args[0] != "" {
		candidates = append(candidates, resolveArg0Candidates(os.Args[0])...)
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", os.ErrNotExist
}

func resolveArg0Candidates(arg0 string) []string {
	if filepath.IsAbs(arg0) {
		return []string{arg0}
	}

	if strings.ContainsRune(arg0, os.PathSeparator) {
		if cwd, err := os.Getwd(); err == nil {
			return []string{filepath.Join(cwd, arg0)}
		}
		return nil
	}

	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		out = append(out, filepath.Join(dir, arg0))
	}
	return out
}
