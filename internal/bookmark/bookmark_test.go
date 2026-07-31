package bookmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bm(path string, line int, at time.Time) Bookmark {
	return Bookmark{
		Path: path, Line: line,
		Context: []string{"before", "the line", "after"}, Offset: 1,
		At: at, LastSeen: at,
	}
}

func keys(s *Store) []string {
	out := make([]string, len(s.Bookmarks))
	for i, b := range s.Bookmarks {
		out[i] = b.Path
	}
	return out
}

const forever = 0 // a non-positive retention keeps everything

func TestAddIsNewestFirstAndDeduplicatesByLine(t *testing.T) {
	now := time.Now()
	s := New("/repo")
	s.Add(bm("a.go", 1, now), 100)
	s.Add(bm("b.go", 2, now.Add(time.Minute)), 100)

	if got := strings.Join(keys(s), ","); got != "b.go,a.go" {
		t.Errorf("order = %v, want newest first", keys(s))
	}

	// The same place again is the same bookmark, moved to the front.
	s.Add(bm("a.go", 1, now.Add(2*time.Minute)), 100)
	if got := strings.Join(keys(s), ","); got != "a.go,b.go" {
		t.Errorf("after re-bookmarking = %v", keys(s))
	}
	// A different line in the same file is a different bookmark.
	s.Add(bm("a.go", 9, now.Add(3*time.Minute)), 100)
	if len(s.Bookmarks) != 3 {
		t.Errorf("a second line in a.go should be its own entry: %v", s.Bookmarks)
	}
}

func TestAddCapsTheList(t *testing.T) {
	now := time.Now()
	s := New("/repo")
	for i := range 5 {
		s.Add(bm("f.go", i+1, now.Add(time.Duration(i)*time.Minute)), 2)
	}
	if len(s.Bookmarks) != 2 || s.Bookmarks[0].Line != 5 {
		t.Errorf("capped list = %+v, want the two newest", s.Bookmarks)
	}
}

func TestRemove(t *testing.T) {
	s := New("/repo")
	s.Add(bm("a.go", 1, time.Now()), 100)

	if !s.Remove("a.go", 1) {
		t.Error("removing a present bookmark should report true")
	}
	if s.Remove("a.go", 1) {
		t.Error("removing it twice should report false")
	}
	if len(s.Bookmarks) != 0 {
		t.Errorf("left %v", s.Bookmarks)
	}
}

// Deleting has to bypass the merge. Save unions with the file so two processes
// adding bookmarks cannot clobber each other — which means it would also read a
// just-forgotten bookmark straight back out and undo the delete.
func TestRemoveNeedsOverwriteBecauseSaveWouldResurrectIt(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s := New("/repo")
	s.Add(bm("a.go", 1, now), 100)
	s.Add(bm("b.go", 2, now), 100)
	if err := s.Save(dir, 100, now, forever); err != nil {
		t.Fatal(err)
	}

	// The wrong way round: remove, then merge-save.
	viaSave := Load(dir, "/repo")
	viaSave.Remove("a.go", 1)
	if err := viaSave.Save(dir, 100, now, forever); err != nil {
		t.Fatal(err)
	}
	if got := keys(Load(dir, "/repo")); len(got) != 2 {
		t.Errorf("Save after Remove left %v — if this ever drops to one, "+
			"Overwrite is no longer needed for deletion", got)
	}

	// The right way.
	viaOverwrite := Load(dir, "/repo")
	viaOverwrite.Remove("a.go", 1)
	if err := viaOverwrite.Overwrite(dir); err != nil {
		t.Fatal(err)
	}
	if got := keys(Load(dir, "/repo")); strings.Join(got, ",") != "b.go" {
		t.Errorf("after Overwrite = %v, want just b.go", got)
	}
}

// Expiry runs off LastSeen, so a file that vanishes with a branch switch and
// comes back simply restarts the clock — no flag to get stuck.
func TestExpireUsesLastSeen(t *testing.T) {
	now := time.Now()
	retention := 30 * 24 * time.Hour
	s := New("/repo")
	s.Add(bm("fresh.go", 1, now.Add(-time.Hour)), 100)
	s.Add(bm("stale.go", 2, now.Add(-90*24*time.Hour)), 100)

	// Old capture, seen yesterday: kept, because it is still around.
	old := bm("old-but-present.go", 3, now.Add(-365*24*time.Hour))
	old.LastSeen = now.Add(-24 * time.Hour)
	s.Add(old, 100)

	if n := s.Expire(now, retention); n != 1 {
		t.Errorf("expired %d, want 1", n)
	}
	got := strings.Join(keys(s), ",")
	if strings.Contains(got, "stale.go") {
		t.Errorf("stale bookmark survived: %v", keys(s))
	}
	if !strings.Contains(got, "old-but-present.go") {
		t.Errorf("a bookmark seen yesterday was expired on capture age: %v", keys(s))
	}
}

func TestExpireIsDisabledByANonPositiveWindow(t *testing.T) {
	now := time.Now()
	s := New("/repo")
	s.Add(bm("ancient.go", 1, now.Add(-10*365*24*time.Hour)), 100)

	if n := s.Expire(now, forever); n != 0 {
		t.Errorf("expired %d with retention off", n)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	s := New("/repo")
	s.Add(bm("internal/app/view.go", 42, now), 100)

	if err := s.Save(dir, 100, now, forever); err != nil {
		t.Fatal(err)
	}
	got := Load(dir, "/repo")
	if len(got.Bookmarks) != 1 {
		t.Fatalf("loaded %+v", got.Bookmarks)
	}
	b := got.Bookmarks[0]
	if b.Path != "internal/app/view.go" || b.Line != 42 || b.Text() != "the line" {
		t.Errorf("round trip lost detail: %+v", b)
	}
	if !b.At.Equal(now) {
		t.Errorf("At = %v, want %v", b.At, now)
	}
}

func TestLoadMissingCorruptOrWrongRepo(t *testing.T) {
	dir := t.TempDir()
	if s := Load(dir, "/nope"); len(s.Bookmarks) != 0 || s.Repo != "/nope" {
		t.Errorf("missing file gave %+v", s)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName("/bad")), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Load(dir, "/bad"); len(s.Bookmarks) != 0 {
		t.Errorf("corrupt file gave %+v", s)
	}

	other := New("/elsewhere")
	other.Add(bm("a.go", 1, time.Now()), 100)
	data, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName("/bad")), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Load(dir, "/bad"); len(s.Bookmarks) != 0 {
		t.Error("a store naming a different repo should not be adopted")
	}
}

// "ft bookmark" is its own process and may run while a filetree holds its own
// copy, so a save has to merge rather than overwrite.
func TestSaveMergesAnotherProcessesWrites(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	theirs := New("/repo")
	theirs.Add(bm("theirs.go", 1, now.Add(time.Minute)), 100)
	if err := theirs.Save(dir, 100, now, forever); err != nil {
		t.Fatal(err)
	}

	mine := New("/repo") // loaded before theirs was written
	mine.Add(bm("mine.go", 2, now), 100)
	if err := mine.Save(dir, 100, now, forever); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(keys(Load(dir, "/repo")), ","); got != "theirs.go,mine.go" {
		t.Errorf("merged = %v, want both newest first", keys(Load(dir, "/repo")))
	}
	if got := strings.Join(keys(mine), ","); got != "theirs.go,mine.go" {
		t.Errorf("in-memory copy after save = %v, want the merged list", keys(mine))
	}
}

// A sighting is worth keeping even when it comes from the older capture:
// otherwise a second filetree that noticed the file still exists would have
// its evidence thrown away, and the bookmark would expire early.
func TestSaveKeepsTheLatestSightingAndTheNewestCapture(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	old := New("/repo")
	seenRecently := bm("a.go", 1, now.Add(-time.Hour))
	seenRecently.LastSeen = now
	old.Add(seenRecently, 100)
	if err := old.Save(dir, 100, now, forever); err != nil {
		t.Fatal(err)
	}

	fresh := New("/repo")
	captured := bm("a.go", 1, now) // newer capture, older sighting
	captured.LastSeen = now.Add(-2 * time.Hour)
	fresh.Add(captured, 100)
	if err := fresh.Save(dir, 100, now, forever); err != nil {
		t.Fatal(err)
	}

	got := Load(dir, "/repo")
	if len(got.Bookmarks) != 1 {
		t.Fatalf("merge duplicated the entry: %+v", got.Bookmarks)
	}
	if !got.Bookmarks[0].At.Equal(now) {
		t.Errorf("At = %v, want the newer capture", got.Bookmarks[0].At)
	}
	if !got.Bookmarks[0].LastSeen.Equal(now) {
		t.Errorf("LastSeen = %v, want the later sighting", got.Bookmarks[0].LastSeen)
	}
}

func TestSaveExpiresOnTheWayOut(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s := New("/repo")
	s.Add(bm("fresh.go", 1, now), 100)
	s.Add(bm("stale.go", 2, now.Add(-90*24*time.Hour)), 100)

	if err := s.Save(dir, 100, now, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}

	if got := keys(Load(dir, "/repo")); len(got) != 1 || got[0] != "fresh.go" {
		t.Errorf("saved = %v, want the stale one dropped", got)
	}
}

// Files belonging to no repository share one store rather than being lost.
func TestGlobalStoreHasItsOwnName(t *testing.T) {
	if got := FileName(""); got != "global.json" {
		t.Errorf("global store file = %q", got)
	}
	if FileName("") == FileName("/repo") {
		t.Error("the global store must not collide with a repo's")
	}
	// Canonicalising the global key must leave it alone: EvalSymlinks("")
	// answers ".", which would hash the current directory and quietly give the
	// no-repository bucket a second, unrelated home.
	if got := Canonical(""); got != "" {
		t.Errorf("Canonical(\"\") = %q, want it left empty", got)
	}
}

func TestLoadAllReadsEveryStore(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for _, repo := range []string{"/a", "/b", ""} {
		s := New(repo)
		s.Add(bm("f.go", 1, now), 100)
		if err := s.Save(dir, 100, now, forever); err != nil {
			t.Fatal(err)
		}
	}
	// A file that is not a store, and one filed under the wrong name.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	misfiled, err := json.Marshal(New("/c"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wrong-name.json"), misfiled, 0o644); err != nil {
		t.Fatal(err)
	}

	got := LoadAll(dir)

	if len(got) != 3 {
		t.Fatalf("loaded %d stores, want 3", len(got))
	}
	for _, s := range got {
		if len(s.Bookmarks) != 1 {
			t.Errorf("store %q has %d bookmarks", s.Repo, len(s.Bookmarks))
		}
	}
}
