package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
)

// reloadModel is a model whose config lives in a real file, so a test can edit
// it the way a user would and ask ft to re-read it.
func reloadModel(t *testing.T, body string) *Model {
	t.Helper()
	m := rootedModel(t, t.TempDir())
	m.mode = modeNormal
	m.cfgPath = filepath.Join(t.TempDir(), "config.toml")
	writeConfig(t, m.cfgPath, body)
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	m.cfg = cfg
	m.buildBindings()
	return m
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const minimalConfig = "[commands]\ndefault = \"edit\"\n[commands.edit]\nrun = \"true\"\nmode = \"interactive\"\nkey = \"e\"\n"

// The loop this key exists for: edit the file somewhere ft cannot see, then ask
// ft to pick it up. Nothing else can do this — F5 re-reads the tree, and "C"
// only reloads when the editor it launched exits.
func TestReloadConfigKeyPicksUpAnEdit(t *testing.T) {
	m := reloadModel(t, minimalConfig)
	if got := m.actionKeys["rename"]; got != "R" {
		t.Fatalf("rename = %q before the edit, want R", got)
	}

	writeConfig(t, m.cfgPath, minimalConfig+"\n[keys]\nrename = \"f2\"\n")
	if got := m.actionKeys["rename"]; got != "R" {
		t.Fatalf("rename = %q with the file changed but unread, want R still", got)
	}

	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}); cmd == nil {
		t.Fatal("alt+c produced no command")
	}
	if got := m.actionKeys["rename"]; got != "f2" {
		t.Errorf("rename = %q after alt+c, want f2", got)
	}
	if !strings.Contains(m.statusMsg, "Config reloaded") {
		t.Errorf("status = %q, want it to confirm the reload", m.statusMsg)
	}
}

// A config that no longer parses must not cost ft the one it is running: the
// alternative is a tree with no keys at all until you restart it, which is a
// steep price for a stray bracket.
func TestReloadConfigKeepsTheOldConfigOnAParseError(t *testing.T) {
	m := reloadModel(t, minimalConfig)
	writeConfig(t, m.cfgPath, "[commands\nthis is not toml")

	m.reloadConfig()

	if _, ok := m.cfg.Commands["edit"]; !ok {
		t.Error("a broken config dropped the commands that were working")
	}
	if got := m.actionKeys["rename"]; got != "R" {
		t.Errorf("rename = %q after a failed reload, want R", got)
	}
	if !strings.Contains(m.statusMsg, "config:") || !m.statusErr {
		t.Errorf("status = %q (err=%v), want the parse error reported", m.statusMsg, m.statusErr)
	}
}

// The reload is also when a newly written clash should surface — it is the
// moment the user is looking at the keys they just changed.
func TestReloadConfigReportsAClashInTheNewFile(t *testing.T) {
	m := reloadModel(t, minimalConfig)
	writeConfig(t, m.cfgPath, minimalConfig+"\n[keys]\nworktree-new = \"R\"\n")

	m.reloadConfig()

	if !mentions(m.keyConflicts, "R", "keys.worktree-new") {
		t.Errorf("the clash was not reported on reload: %v", m.keyConflicts)
	}
	if !strings.Contains(m.statusMsg, "Config reloaded") || !strings.Contains(m.statusMsg, "conflict") {
		t.Errorf("status = %q, want the reload and the conflict", m.statusMsg)
	}
}

// The bug that prompted the key. "C" runs commands.default against the config
// file and asks for a reload when it finishes. A background command — a
// hand-off that types the path into another pane and exits — finishes at once,
// so reloading there reads the file exactly as it was and the message claims
// something that cannot have happened.
func TestEditConfigOnlyReloadsWhenTheEditorReallyFinished(t *testing.T) {
	m := reloadModel(t, minimalConfig)
	writeConfig(t, m.cfgPath, minimalConfig+"\n[keys]\nrename = \"f2\"\n")

	m.handleCmdDone(cmdDoneMsg{name: "edit-config", reloadConfig: true, interactive: false})
	if got := m.actionKeys["rename"]; got != "R" {
		t.Errorf("a background hand-off reloaded the config: rename = %q, want R", got)
	}
	if strings.Contains(m.statusMsg, "Config reloaded") {
		t.Errorf("status = %q, want no claim of a reload", m.statusMsg)
	}
	if !strings.Contains(m.statusMsg, m.actionKeys["reload-config"]) {
		t.Errorf("status = %q, want it to name the reload key", m.statusMsg)
	}

	// An interactive editor has finished by the time it returns, so that one
	// still reloads on its own.
	m.handleCmdDone(cmdDoneMsg{name: "edit-config", reloadConfig: true, interactive: true})
	if got := m.actionKeys["rename"]; got != "f2" {
		t.Errorf("rename = %q after an interactive edit, want f2", got)
	}
}
