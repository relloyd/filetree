package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func paths(r *Recent) []string {
	out := make([]string, len(r.Files))
	for i, f := range r.Files {
		out[i] = f.Path
	}
	return out
}

func TestRecentAddOrdersAndDeduplicates(t *testing.T) {
	now := time.Now()
	r := NewRecent("/root")
	for i, p := range []string{"a.go", "b.go", "c.go"} {
		r.Add(p, now.Add(time.Duration(i)*time.Minute), 100)
	}
	if got := paths(r); strings.Join(got, ",") != "c.go,b.go,a.go" {
		t.Errorf("order = %v, want newest first", got)
	}

	// Reopening a file moves it, rather than listing it twice.
	r.Add("a.go", now.Add(9*time.Minute), 100)
	if got := paths(r); strings.Join(got, ",") != "a.go,c.go,b.go" {
		t.Errorf("after reopening a.go: %v", got)
	}
}

func TestRecentCapDropsTheOldest(t *testing.T) {
	now := time.Now()
	r := NewRecent("/root")
	for i, p := range []string{"a.go", "b.go", "c.go", "d.go"} {
		r.Add(p, now.Add(time.Duration(i)*time.Minute), 2)
	}
	if got := paths(r); strings.Join(got, ",") != "d.go,c.go" {
		t.Errorf("capped list = %v, want the two newest", got)
	}
}

func TestRecentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	r := NewRecent("/some/root")
	r.Add("internal/app/view.go", now, 100)
	if err := r.Save(dir, 100); err != nil {
		t.Fatal(err)
	}

	got := LoadRecent(dir, "/some/root")
	if len(got.Files) != 1 || got.Files[0].Path != "internal/app/view.go" {
		t.Fatalf("loaded %+v", got.Files)
	}
	if !got.Files[0].At.Equal(now) {
		t.Errorf("timestamp = %v, want %v", got.Files[0].At, now)
	}
}

func TestLoadRecentMissingCorruptOrWrongRoot(t *testing.T) {
	dir := t.TempDir()
	if r := LoadRecent(dir, "/nope"); len(r.Files) != 0 || r.Root != "/nope" {
		t.Errorf("missing file should yield an empty history, got %+v", r)
	}
	if err := os.WriteFile(filepath.Join(dir, RecentFileName("/bad")), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := LoadRecent(dir, "/bad"); len(r.Files) != 0 {
		t.Errorf("corrupt file should yield an empty history, got %+v", r)
	}
	// A file naming a different root is a stale one left behind by a rename:
	// its paths are relative to a tree this is not, so it is not ours to use.
	other := NewRecent("/elsewhere")
	other.Add("a.go", time.Now(), 100)
	data, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RecentFileName("/bad")), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := LoadRecent(dir, "/bad"); len(r.Files) != 0 {
		t.Errorf("wrong-root file should yield an empty history, got %+v", r)
	}
}

// Two ft sessions on one root each hold their own copy, so saving has to merge:
// a plain overwrite would drop whatever the other session had opened.
func TestRecentSaveMergesAnotherSessionsWrites(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Truncate(time.Second)

	mine := NewRecent("/root")
	mine.Add("mine.go", base, 100)

	theirs := NewRecent("/root")
	theirs.Add("theirs.go", base.Add(time.Minute), 100)
	if err := theirs.Save(dir, 100); err != nil {
		t.Fatal(err)
	}

	if err := mine.Save(dir, 100); err != nil {
		t.Fatal(err)
	}
	if got := paths(LoadRecent(dir, "/root")); strings.Join(got, ",") != "theirs.go,mine.go" {
		t.Errorf("merged history = %v, want both entries newest first", got)
	}
	// The merge is folded back in, so the saving session is not left holding a
	// list it would overwrite the merge with on its next save.
	if got := paths(mine); strings.Join(got, ",") != "theirs.go,mine.go" {
		t.Errorf("in-memory history after save = %v, want the merged list", got)
	}
}

func TestRecentSaveKeepsTheNewerTimestamp(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Truncate(time.Second)

	old := NewRecent("/root")
	old.Add("a.go", base, 100)
	if err := old.Save(dir, 100); err != nil {
		t.Fatal(err)
	}

	fresh := NewRecent("/root")
	fresh.Add("a.go", base.Add(time.Hour), 100)
	if err := fresh.Save(dir, 100); err != nil {
		t.Fatal(err)
	}

	got := LoadRecent(dir, "/root")
	if len(got.Files) != 1 {
		t.Fatalf("merge duplicated a path: %+v", got.Files)
	}
	if !got.Files[0].At.Equal(base.Add(time.Hour)) {
		t.Errorf("timestamp = %v, want the newer one", got.Files[0].At)
	}
}

func TestRecentFileNameSitsBesideTheStateFile(t *testing.T) {
	if got, want := RecentFileName("/a/proj"), strings.TrimSuffix(FileName("/a/proj"), ".json")+".recent.json"; got != want {
		t.Errorf("RecentFileName = %q, want %q", got, want)
	}
	if RecentFileName("/a/proj") == FileName("/a/proj") {
		t.Error("history and state must not share a file name")
	}
}
