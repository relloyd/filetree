//go:build darwin

package platform

import (
	"fmt"
	"os/exec"
	"strings"
)

type darwin struct{}

func New() Platform { return darwin{} }

func (darwin) CopyToClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pbcopy: %w", err)
	}
	return nil
}

func (darwin) OpenURL(url string) error {
	if out, err := exec.Command("open", url).CombinedOutput(); err != nil {
		return fmt.Errorf("open: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (darwin) Reveal(path string) error {
	if out, err := exec.Command("open", "-R", path).CombinedOutput(); err != nil {
		return fmt.Errorf("open -R: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (darwin) Trash(path string) error {
	// Finder's delete puts the file in the Trash with "Put Back" support,
	// unlike a plain unlink. %q produces escaping AppleScript accepts.
	script := fmt.Sprintf(`tell application "Finder" to delete POSIX file %q`, path)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("move to trash: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
