package main

// Shared ground for the keys that drive herdr: working out which place the
// selection belongs to, and which of herdr's panes are part of it.
//
// Both keys ask the same two questions before they differ. "J" then wants the
// idle shells among the answer; the editor key wants whichever pane is already
// running helix. Keeping the scoping here means the two can never drift into
// disagreeing about what "this place" is.

import (
	"path/filepath"

	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/herdr"
)

// checkoutFor is the checkout dir belongs to, ignoring a repository that spans
// the whole home directory.
//
// Keeping dotfiles in a repository at ~ is common, and it makes gitx.FindRepoRoot
// answer "$HOME" for every loose directory on the machine. That would be one
// enormous checkout: asking for a shell in ~/Downloads would hand you the one
// sitting in ~/some-other-thing, because both "belong to the same checkout".
// A repository that large is not a project, so it is not a scope either, and the
// selection falls back to being scoped by its own directory.
//
// home is passed in rather than looked up so this stays testable without
// touching the real one.
func checkoutFor(dir, home string) string {
	root := gitx.FindRepoRoot(dir)
	if root == "" {
		return ""
	}
	if home != "" && filepath.Clean(home) == root {
		return ""
	}
	return root
}

// scopedPane is a pane that belongs to the selection's scope, with the checkout
// it resolved to kept alongside so it is only worked out once.
type scopedPane struct {
	pane     herdr.Pane
	checkout string
}

// inScope narrows the server's panes to the ones belonging to scope.
//
// ft's own pane is excluded. The tmux hand-off commands do the same by matching
// $TMUX_PANE, and for the same reason: focusing ourselves would look like the
// key did nothing at all.
//
// Agent panes and busy shells stay in, because this list answers two questions.
// Which shell to focus needs only the idle ones, and atPrompt narrows to those.
// Which workspace the code lives in is answered by any pane at all — a lone
// agent working on a repository still says where that repository lives.
func inScope(panes []herdr.Pane, scope herdr.Scope, self, home string) []scopedPane {
	roots := map[string]string{} // working directory -> checkout root
	var out []scopedPane
	for _, p := range panes {
		if p.ID == "" || p.ID == self {
			continue
		}
		cwd := p.Cwd()
		if cwd == "" {
			continue
		}
		root, seen := roots[cwd]
		if !seen {
			root = checkoutFor(cwd, home)
			roots[cwd] = root
		}
		if !scope.Covers(root, cwd) {
			continue
		}
		out = append(out, scopedPane{pane: p, checkout: root})
	}
	return out
}

// atPrompt narrows scoped panes to the shells that could take a command right
// now: no agent in them, and nothing running on top of the shell.
//
// The pane.process_info call is one round trip each, which is why it is paid
// last — only for panes already known to be in the right place, and never for
// one holding an agent.
func atPrompt(c *herdr.Client, scoped []scopedPane) []herdr.Shell {
	var out []herdr.Shell
	for _, s := range scoped {
		// A pane herdr has recognised an agent in is not a shell to type at,
		// whatever its working directory says.
		if s.pane.Agent != "" {
			continue
		}
		info, err := c.ProcessInfo(s.pane.ID)
		if err != nil || !info.AtPrompt() {
			continue
		}
		out = append(out, herdr.Shell{
			PaneID:   s.pane.ID,
			TabID:    s.pane.TabID,
			Dir:      s.pane.Cwd(),
			Checkout: s.checkout,
		})
	}
	return out
}

// workspaceFor picks the workspace this place already lives in, if any.
//
// ft's own comes first when the code is there too, which is what keeps a shell
// for the repository the tree is showing beside the tree rather than a tab away.
// Otherwise the lowest pane id decides, so a place spread across two workspaces
// resolves the same way on every press.
func workspaceFor(scoped []scopedPane, selfWorkspace string) (string, bool) {
	if selfWorkspace != "" {
		for _, s := range scoped {
			if s.pane.WorkspaceID == selfWorkspace {
				return selfWorkspace, true
			}
		}
	}
	best, ws := "", ""
	for _, s := range scoped {
		if s.pane.WorkspaceID == "" {
			continue
		}
		if best == "" || s.pane.ID < best {
			best, ws = s.pane.ID, s.pane.WorkspaceID
		}
	}
	return ws, ws != ""
}

// abbrev names a directory by where it sits in the scope, which is what the
// status bar has room for and what says whether the right shell was chosen. The
// scope root itself reads as its own basename rather than as ".".
func abbrev(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "" {
		return dir
	}
	if rel == "." {
		return filepath.Base(root)
	}
	return rel
}
