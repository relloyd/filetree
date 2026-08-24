package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// revealModel is rootModel's project with a dot-directory and a build
// directory to hide things in:
//
//	project/sub/a/b/deep.txt
//	project/other/x.txt
//	project/top.txt
//	project/.secret/note.txt
//	project/visible/.hidden.txt
func revealModel(t *testing.T) (*Model, string) {
	t.Helper()
	m, project := rootModel(t)
	for _, d := range []string{".secret", "visible"} {
		if err := os.MkdirAll(filepath.Join(project, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{".secret/note.txt", "visible/.hidden.txt"} {
		if err := os.WriteFile(filepath.Join(project, filepath.FromSlash(f)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = m.tr.Refresh(m.tr.Root)
	m.reflatten()
	return m, project
}

// ask runs one jump request, the way the socket listener would.
func ask(m *Model, path string) RevealResult {
	reply := make(chan RevealResult, 1)
	m.handleReveal(RevealMsg{Path: path, Reply: reply})
	return <-reply
}

// The whole point: a file several collapsed directories down becomes the
// selection.
func TestRevealSelectsADeepFile(t *testing.T) {
	m, project := revealModel(t)
	deep := filepath.Join(project, "sub", "a", "b", "deep.txt")

	res := ask(m, deep)
	if !res.OK {
		t.Fatalf("reveal refused: %s", res.Reason)
	}
	if sel := m.selected(); sel == nil || sel.Path != deep {
		t.Errorf("selection = %v, want %s", sel, deep)
	}
}

func TestRevealSelectsAFileAtTheTop(t *testing.T) {
	m, project := revealModel(t)
	top := filepath.Join(project, "top.txt")
	if res := ask(m, top); !res.OK {
		t.Fatalf("reveal refused: %s", res.Reason)
	}
	if sel := m.selected(); sel == nil || sel.Path != top {
		t.Errorf("selection = %v, want %s", sel, top)
	}
}

func TestRevealSelectsADirectory(t *testing.T) {
	m, project := revealModel(t)
	dir := filepath.Join(project, "sub", "a")
	if res := ask(m, dir); !res.OK {
		t.Fatalf("reveal refused: %s", res.Reason)
	}
	if sel := m.selected(); sel == nil || sel.Path != dir {
		t.Errorf("selection = %v, want %s", sel, dir)
	}
}

func TestRevealRejectsAPathOutsideTheRoot(t *testing.T) {
	m, project := revealModel(t)
	outside := filepath.Join(filepath.Dir(project), "elsewhere.txt")

	res := ask(m, outside)
	if res.OK {
		t.Fatal("reveal accepted a path outside the root")
	}
	if !strings.Contains(res.Reason, "outside") {
		t.Errorf("reason = %q, want it to say the path is outside the root", res.Reason)
	}
}

func TestRevealRejectsAMissingFile(t *testing.T) {
	m, project := revealModel(t)
	res := ask(m, filepath.Join(project, "sub", "gone.txt"))
	if res.OK {
		t.Fatal("reveal accepted a path that does not exist")
	}
	if res.Reason == "" {
		t.Error("no reason given")
	}
}

func TestRevealRejectsARelativePath(t *testing.T) {
	m, _ := revealModel(t)
	if res := ask(m, "sub/a/b/deep.txt"); res.OK {
		t.Fatal("reveal accepted a relative path")
	}
}

func TestRevealRejectsAnEmptyPath(t *testing.T) {
	m, _ := revealModel(t)
	if res := ask(m, ""); res.OK {
		t.Fatal("reveal accepted an empty path")
	}
}

// The failure this guards is the quiet one: selectPath no-ops on a path with
// no row, so a reveal that did not check would report success while nothing
// moved.
func TestRevealRefusesAHiddenFileAndNamesTheKey(t *testing.T) {
	m, project := revealModel(t)
	m.showHidden = false
	m.reflatten()
	before := m.cursor

	res := ask(m, filepath.Join(project, "visible", ".hidden.txt"))
	if res.OK {
		t.Fatal("reveal claimed to show a hidden file")
	}
	if !strings.Contains(res.Reason, "hidden") {
		t.Errorf("reason = %q, want it to say hidden", res.Reason)
	}
	if key := m.actionKeys["toggle-hidden"]; key != "" && !strings.Contains(res.Reason, key) {
		t.Errorf("reason = %q, want it to name %q", res.Reason, key)
	}
	if m.cursor != before {
		t.Errorf("cursor moved to %d despite the refusal", m.cursor)
	}
}

// A perfectly ordinary file inside a dot-directory has no row either, because
// Flatten skips the whole subtree. Naming the file would send the user looking
// for a property it does not have.
func TestRevealBlamesTheHiddenAncestorNotTheFile(t *testing.T) {
	m, project := revealModel(t)
	m.showHidden = false
	m.reflatten()

	res := ask(m, filepath.Join(project, ".secret", "note.txt"))
	if res.OK {
		t.Fatal("reveal claimed to show a file under a dot-directory")
	}
	if !strings.Contains(res.Reason, ".secret") {
		t.Errorf("reason = %q, want it to blame .secret", res.Reason)
	}
	if strings.Contains(res.Reason, "note.txt") {
		t.Errorf("reason = %q, should not blame the file itself", res.Reason)
	}
}

// With hidden files shown, the same two paths must succeed — the refusal above
// is about the filter, not about the paths.
func TestRevealFindsHiddenPathsOnceShown(t *testing.T) {
	m, project := revealModel(t)
	m.showHidden = true
	m.reflatten()

	for _, p := range []string{
		filepath.Join(project, ".secret", "note.txt"),
		filepath.Join(project, "visible", ".hidden.txt"),
	} {
		if res := ask(m, p); !res.OK {
			t.Errorf("reveal(%s) refused: %s", p, res.Reason)
		} else if sel := m.selected(); sel == nil || sel.Path != p {
			t.Errorf("selection = %v, want %s", sel, p)
		}
	}
}

// commitPrompt resolves a rename target from m.selected() when it is
// committed, not when the prompt opens. A jump landing in between would rename
// a file the user never chose.
func TestRevealWaitsWhileAPromptIsOpen(t *testing.T) {
	m, project := revealModel(t)
	top := filepath.Join(project, "top.txt")
	selectPathOrFail(t, m, top)
	before := m.cursor

	m.mode = modePrompt
	res := ask(m, filepath.Join(project, "sub", "a", "b", "deep.txt"))
	if res.OK {
		t.Fatal("reveal moved the cursor during a prompt")
	}
	if m.cursor != before {
		t.Errorf("cursor moved to %d during a prompt", m.cursor)
	}
	if sel := m.selected(); sel == nil || sel.Path != top {
		t.Errorf("selection = %v, want the prompt's target %s", sel, top)
	}
}

// The finder acts on its own selection, not the tree cursor, so a jump behind
// the overlay is safe and lands when you leave it.
func TestRevealWorksBehindTheFinder(t *testing.T) {
	m, project := revealModel(t)
	m.mode = modeFuzzy
	deep := filepath.Join(project, "sub", "a", "b", "deep.txt")
	if res := ask(m, deep); !res.OK {
		t.Fatalf("reveal refused behind the finder: %s", res.Reason)
	}
	if sel := m.selected(); sel == nil || sel.Path != deep {
		t.Errorf("selection = %v, want %s", sel, deep)
	}
}

// A client that has already timed out leaves nobody receiving. The buffered
// channel plus the non-blocking send is what stops that wedging Update; this
// checks the nil case, which is the other way a caller can opt out.
func TestRevealWithNoReplyChannel(t *testing.T) {
	m, project := revealModel(t)
	deep := filepath.Join(project, "sub", "a", "b", "deep.txt")
	m.handleReveal(RevealMsg{Path: deep})
	if sel := m.selected(); sel == nil || sel.Path != deep {
		t.Errorf("selection = %v, want %s", sel, deep)
	}
}

// Re-rooting has to reach the listener, or a jump would be routed against a
// root the tree left behind.
func TestRootObserverSeesEveryReRoot(t *testing.T) {
	m, project := revealModel(t)
	var seen []string
	m.SetRootObserver(func(root string) { seen = append(seen, root) })

	if len(seen) != 1 || seen[0] != project {
		t.Fatalf("observer saw %v on registration, want [%s]", seen, project)
	}
	sub := filepath.Join(project, "sub")
	if _, cmd := m.enterView(sub, nil); cmd == nil {
		_ = cmd
	}
	if m.tr.Root.Path != sub {
		t.Fatalf("root = %s, want %s", m.tr.Root.Path, sub)
	}
	if len(seen) != 2 || seen[1] != sub {
		t.Fatalf("observer saw %v, want the new root %s", seen, sub)
	}
	if _, cmd := m.leaveView(); cmd == nil {
		_ = cmd
	}
	if len(seen) != 3 || seen[2] != project {
		t.Errorf("observer saw %v, want the trip home to %s", seen, project)
	}
}

// A reveal after a re-root must be judged against the new root, not the old.
func TestRevealAfterReRoot(t *testing.T) {
	m, project := revealModel(t)
	sub := filepath.Join(project, "sub")
	m.enterView(sub, nil)

	if res := ask(m, filepath.Join(project, "top.txt")); res.OK {
		t.Error("reveal accepted a path above the new root")
	}
	deep := filepath.Join(sub, "a", "b", "deep.txt")
	if res := ask(m, deep); !res.OK {
		t.Fatalf("reveal refused a path under the new root: %s", res.Reason)
	}
	if sel := m.selected(); sel == nil || sel.Path != deep {
		t.Errorf("selection = %v, want %s", sel, deep)
	}
}
