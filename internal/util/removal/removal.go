// Package removal deletes files and directories only after an explicit
// confirmation, and never deletes the process working directory, its ancestors,
// the user's home directory, or a filesystem root.
package removal

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrProtected reports a path that is never removed, regardless of confirmation.
var ErrProtected = errors.New("refusing to remove protected path")

// ErrDeclined reports that the confirmer did not approve the removal.
var ErrDeclined = errors.New("removal not confirmed")

// Confirmer reports whether path may be removed.
type Confirmer func(path string) bool

// Remover removes paths only after Confirm approves each one.
type Remover struct {
	Confirm Confirmer
}

// Interactive asks on the controlling terminal before every removal. Without a
// terminal nothing is removed.
var Interactive = Remover{Confirm: AskTerminal}

// Always approves every removal. Intended for tests.
var Always = Remover{Confirm: func(string) bool { return true }}

// Never declines every removal.
var Never = Remover{Confirm: func(string) bool { return false }}

// Remove deletes a single file or empty directory after confirmation.
func (r Remover) Remove(path string) error {
	return r.remove(path, os.Remove)
}

// RemoveAll deletes path and everything beneath it after confirmation.
func (r Remover) RemoveAll(path string) error {
	return r.remove(path, os.RemoveAll)
}

func (r Remover) remove(path string, deleteFn func(string) error) error {
	abs, err := guard(path)
	if err != nil {
		return err
	}
	if r.Confirm == nil || !r.Confirm(abs) {
		return fmt.Errorf("%w: %s", ErrDeclined, abs)
	}
	return deleteFn(abs)
}

// guard resolves path and rejects anything that must never be removed.
func guard(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: empty path", ErrProtected)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if isRoot(abs) {
		return abs, fmt.Errorf("%w: %s", ErrProtected, abs)
	}
	for _, dir := range protectedDirs() {
		if abs == dir || strings.HasPrefix(dir, abs+string(filepath.Separator)) {
			return abs, fmt.Errorf("%w: %s", ErrProtected, abs)
		}
	}
	return abs, nil
}

func isRoot(abs string) bool {
	return abs == filepath.VolumeName(abs)+string(filepath.Separator)
}

// protectedDirs lists the working directory and home directory, each in both
// its literal and symlink-resolved form.
func protectedDirs() []string {
	var dirs []string
	for _, candidate := range []func() (string, error){os.Getwd, os.UserHomeDir} {
		dir, err := candidate()
		if err != nil || dir == "" {
			continue
		}
		dirs = append(dirs, filepath.Clean(dir))
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dirs = append(dirs, filepath.Clean(resolved))
		}
	}
	return dirs
}

// AskTerminal prompts on /dev/tty, so it works even when stdin and stdout are
// pipes. It returns false when no terminal is available, leaving path in place.
func AskTerminal(path string) bool {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fabric: left %s in place: no terminal to confirm removal\n", path)
		return false
	}
	defer tty.Close()
	return ask(tty, path)
}

// ask writes the prompt to rw and reads one line; only "y" or "yes" approves.
func ask(rw io.ReadWriter, path string) bool {
	fmt.Fprintf(rw, "fabric: remove %s? [y/N] ", path)
	line, err := bufio.NewReader(rw).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(rw)
		return false
	}
	return approves(line)
}

func approves(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}
