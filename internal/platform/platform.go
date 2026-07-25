// Package platform abstracts OS-specific integrations (clipboard, file
// manager reveal, trash). Implementations live in platform_<goos>.go files
// behind build tags; New returns the one for the current OS.
package platform

type Platform interface {
	// CopyToClipboard places text on the system clipboard.
	CopyToClipboard(text string) error
	// Reveal shows the path in the OS file manager (Finder on macOS).
	Reveal(path string) error
	// OpenURL opens a URL in the default browser.
	OpenURL(url string) error
	// Trash moves the path to the OS trash so it can be recovered.
	Trash(path string) error
}
