package herdr

import "encoding/json"

// Pane is one herdr pane, as pane.list reports it. Only the fields ft actually
// decides on are named; herdr sends a good deal more.
type Pane struct {
	ID          string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`

	// Agent is the coding agent herdr detected in the pane, and is absent for
	// an ordinary shell. It is what keeps a shell hand-off away from a pane
	// running Claude: the pane ft wants is one with nothing on top of the
	// shell, and an agent is exactly that something.
	Agent string `json:"agent"`

	// Dir is where the pane was started; ForegroundDir is where its foreground
	// process is now, which is the truer answer once someone has cd'd
	// somewhere else in the same shell.
	Dir           string `json:"cwd"`
	ForegroundDir string `json:"foreground_cwd"`
}

// Cwd is the pane's working directory, preferring where its foreground process
// actually sits over where the pane was started. Both can be empty for a pane
// herdr cannot inspect, and the caller treats that as "nowhere in particular"
// rather than guessing.
func (p Pane) Cwd() string {
	if p.ForegroundDir != "" {
		return p.ForegroundDir
	}
	return p.Dir
}

// Process is one process running in a pane.
//
// Argv0 is how it was invoked; Name is what it calls itself now, and the two
// part company more often than you would think — see Runs.
type Process struct {
	Name  string `json:"name"`
	Argv0 string `json:"argv0"`
	PID   int    `json:"pid"`
	Dir   string `json:"cwd"`
}

// ProcessInfo is what pane.process_info reports: the pane's login shell and
// whatever currently holds the terminal.
type ProcessInfo struct {
	PaneID         string    `json:"pane_id"`
	ShellPID       int       `json:"shell_pid"`
	ForegroundPGID int       `json:"foreground_process_group_id"`
	Foreground     []Process `json:"foreground_processes"`
}

// AtPrompt reports whether the pane is sitting at an idle shell prompt.
//
// The test is that the foreground process group *is* the shell: when anything
// else holds the terminal — an editor, a dev server, a test watcher — the
// kernel has given the foreground group to that process instead, and the two
// ids differ. It is sharper than matching the running command against a list of
// shell names, which is what ft has to do under tmux, and it needs no such list
// to be kept up to date.
//
// A pane herdr could not inspect reports neither id, and is not at a prompt as
// far as anyone here can tell: better to open a new shell than to type into
// something unknown.
func (p ProcessInfo) AtPrompt() bool {
	return p.ShellPID != 0 && p.ForegroundPGID == p.ShellPID
}

// Runs reports whether the pane is running a program invoked as argv0.
//
// Two details make this fussier than it looks, and both were found by reading
// what a real herdr server says about a real pane.
//
// It matches Argv0 rather than Name, because a program may rename itself once
// started: Claude Code reports its Name as its version number ("2.1.268") while
// its Argv0 stays "claude".
//
// And it scans every foreground process rather than the first, because a
// foreground group can hold several. A pane running Claude under caffeinate
// lists caffeinate *ahead* of it, so taking the first entry would find the
// wrapper and miss the program entirely.
func (p ProcessInfo) Runs(argv0 string) bool {
	if argv0 == "" {
		return false
	}
	for _, proc := range p.Foreground {
		if proc.Argv0 == argv0 || proc.Name == argv0 {
			return true
		}
	}
	return false
}

// Panes lists every pane on the server, across all workspaces.
func (c *Client) Panes() ([]Pane, error) {
	var out struct {
		Panes []Pane `json:"panes"`
	}
	if err := c.call("pane.list", struct{}{}, &out); err != nil {
		return nil, err
	}
	return out.Panes, nil
}

// ProcessInfo reports what is running in one pane.
func (c *Client) ProcessInfo(paneID string) (ProcessInfo, error) {
	var out struct {
		Info ProcessInfo `json:"process_info"`
	}
	err := c.call("pane.process_info", map[string]string{"pane_id": paneID}, &out)
	return out.Info, err
}

// FocusPane moves herdr's focus to a pane, switching tab and workspace as
// needed. The reply carries nothing ft needs, so only the error is read.
func (c *Client) FocusPane(paneID string) error {
	return c.call("pane.focus", map[string]string{"pane_id": paneID}, nil)
}

// SplitPane opens a new pane beside target, running the default shell in dir,
// and focuses it. An empty target splits whichever pane herdr has focused.
//
// Unlike tmux's full-height split, this divides the target pane's own
// rectangle, so no other pane in the tab changes size — which is why ft has
// nothing to measure or restore around it.
func (c *Client) SplitPane(target, dir string) (string, error) {
	params := struct {
		Direction string `json:"direction"`
		Target    string `json:"target_pane_id,omitempty"`
		Dir       string `json:"cwd,omitempty"`
		Focus     bool   `json:"focus"`
	}{Direction: "right", Target: target, Dir: dir, Focus: true}

	var out struct {
		Pane ref `json:"pane"`
	}
	if err := c.call("pane.split", params, &out); err != nil {
		return "", err
	}
	return string(out.Pane), nil
}

// CreateTab opens a new tab with its shell in dir and focuses it. An empty
// workspace puts the tab wherever herdr is focused.
//
// This is how a shell reaches a workspace that already holds the code but has
// no pane worth splitting — somewhere else on screen, so a tab of its own is
// less disruptive than carving up a layout the tree is not even looking at.
func (c *Client) CreateTab(workspace, dir, label string) (string, error) {
	params := struct {
		Workspace string `json:"workspace_id,omitempty"`
		Dir       string `json:"cwd,omitempty"`
		Label     string `json:"label,omitempty"`
		Focus     bool   `json:"focus"`
	}{Workspace: workspace, Dir: dir, Label: label, Focus: true}

	var out struct {
		RootPane ref `json:"root_pane"`
	}
	if err := c.call("tab.create", params, &out); err != nil {
		return "", err
	}
	return string(out.RootPane), nil
}

// CreateWorkspace opens a whole new workspace for dir, named label, and focuses
// its shell.
//
// It is the answer when herdr has nothing for this code at all. A workspace
// rather than a tab beside whatever ft happened to be sitting in: the point of
// the key is to put the code somewhere of its own, and a directory herdr has
// never seen has no home to be added to.
func (c *Client) CreateWorkspace(dir, label string) (pane, tab string, err error) {
	params := struct {
		Dir   string `json:"cwd,omitempty"`
		Label string `json:"label,omitempty"`
		Focus bool   `json:"focus"`
	}{Dir: dir, Label: label, Focus: true}

	var out struct {
		RootPane ref `json:"root_pane"`
		Tab      ref `json:"tab"`
	}
	if err := c.call("workspace.create", params, &out); err != nil {
		return "", "", err
	}
	return string(out.RootPane), string(out.Tab), nil
}

// RenameTab gives a tab a label.
func (c *Client) RenameTab(tabID, label string) error {
	return c.call("tab.rename", map[string]string{"tab_id": tabID, "label": label}, nil)
}

// Tabs lists the tabs on the server, which is how a caller finds out whether a
// tab still carries the positional label herdr gave it.
func (c *Client) Tabs() ([]Tab, error) {
	var out struct {
		Tabs []Tab `json:"tabs"`
	}
	if err := c.call("tab.list", struct{}{}, &out); err != nil {
		return nil, err
	}
	return out.Tabs, nil
}

// Tab is one herdr tab.
type Tab struct {
	ID          string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// Unnamed reports whether the tab still carries the positional label herdr
// gives a new one ("1", "2", ...) rather than a name something chose.
//
// It is what keeps a rename from trampling a label the user set by hand, or one
// herdr derived from what is running — a tab showing "ft" is telling you
// something, and overwriting it with "hx" would not be an improvement.
func (t Tab) Unnamed() bool {
	if t.Label == "" {
		return true
	}
	for _, r := range t.Label {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ref is an identifier herdr has been seen to send either as a bare string or
// as the object it names. prutil hit the same variation and handles it the same
// way; accepting both costs a few lines and removes a whole class of failure
// that would only appear on a herdr upgrade.
type ref string

func (r *ref) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*r = ref(s)
		return nil
	}
	var obj struct {
		PaneID string `json:"pane_id"`
		TabID  string `json:"tab_id"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	switch {
	case obj.PaneID != "":
		*r = ref(obj.PaneID)
	case obj.TabID != "":
		*r = ref(obj.TabID)
	default:
		*r = ref(obj.ID)
	}
	return nil
}

// SendKeys presses keys in a pane. Names are logical and lowercase ("esc",
// "enter", "ctrl+c"), and herdr validates the whole list before writing any
// bytes, so a typo cannot leave half a chord in the terminal.
func (c *Client) SendKeys(paneID string, keys []string) error {
	return c.call("pane.send_keys", struct {
		PaneID string   `json:"pane_id"`
		Keys   []string `json:"keys"`
	}{PaneID: paneID, Keys: keys}, nil)
}

// SendText writes text into a pane as raw keystrokes.
//
// The difference from SendInput is not a detail, and it is the whole reason this
// method is here: SendInput honours the pane's bracketed-paste mode and arrives
// as a *paste*, while SendText arrives as typing.
//
// A program that distinguishes the two will do completely different things with
// them. Sending ":open file" to helix through SendInput pastes the literal text
// into the buffer and leaves the file modified; through SendText the leading
// colon opens helix's command line, which is what was wanted. Verified against a
// real helix rather than reasoned about.
//
// So: SendText to drive a program's own key handling, SendInput to hand a
// command to a shell.
func (c *Client) SendText(paneID, text string) error {
	return c.call("pane.send_text", struct {
		PaneID string `json:"pane_id"`
		Text   string `json:"text"`
	}{PaneID: paneID, Text: text}, nil)
}

// SendInput pastes text into a pane and then presses keys, in that order and as
// one request.
//
// Atomic, which is what makes it right for handing a command line to a shell:
// it is what "herdr pane run" uses to send a command and its Enter together. It
// is the wrong tool for driving a program's key handling — see SendText.
func (c *Client) SendInput(paneID, text string, keys []string) error {
	return c.call("pane.send_input", struct {
		PaneID string   `json:"pane_id"`
		Text   string   `json:"text,omitempty"`
		Keys   []string `json:"keys,omitempty"`
	}{PaneID: paneID, Text: text, Keys: keys}, nil)
}
