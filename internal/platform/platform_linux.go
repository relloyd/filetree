//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type linux struct{}

func New() Platform { return linux{} }

// first returns the first of the named commands found on PATH.
func first(names ...string) (string, bool) {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return n, true
		}
	}
	return "", false
}

func run(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (linux) CopyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch name, _ := first("wl-copy", "xclip", "xsel"); name {
	case "wl-copy":
		cmd = exec.Command("wl-copy")
	case "xclip":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	case "xsel":
		cmd = exec.Command("xsel", "--clipboard", "--input")
	default:
		return errors.New("no clipboard tool found (install wl-clipboard, xclip or xsel)")
	}
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", cmd.Args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (linux) OpenURL(url string) error {
	if _, ok := first("xdg-open"); !ok {
		return errors.New("xdg-open not found")
	}
	return run("xdg-open", url)
}

// Reveal opens the containing directory: there is no portable way to ask
// a Linux file manager to select a single entry.
func (linux) Reveal(path string) error {
	if _, ok := first("xdg-open"); !ok {
		return errors.New("xdg-open not found")
	}
	return run("xdg-open", filepath.Dir(path))
}

// Trash never falls back to unlinking: callers rely on it being
// recoverable, so without a trash tool it refuses.
func (linux) Trash(path string) error {
	switch name, _ := first("gio", "trash-put"); name {
	case "gio":
		return run("gio", "trash", "--", path)
	case "trash-put":
		return run("trash-put", "--", path)
	}
	return errors.New("no trash tool found (install gio via libglib2.0-bin, or trash-cli)")
}

func (linux) Notify(title, message string) error {
	if _, ok := first("notify-send"); !ok {
		return errors.New("notify-send not found")
	}
	// Passed as argv, never through a shell: message comes from a hook payload.
	return run("notify-send", "--", title, message)
}
