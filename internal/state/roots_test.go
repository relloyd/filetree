package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func rootPaths(r *Roots) string {
	out := make([]string, len(r.Roots))
	for i, e := range r.Roots {
		out[i] = e.Path
	}
	return strings.Join(out, ",")
}

func TestRootsVisitOrdersAndDeduplicates(t *testing.T) {
	now := time.Now()
	r := NewRoots("/home")
	for i, p := range []string{"/home", "/home/a", "/home/b"} {
		r.Visit(p, now.Add(time.Duration(i)*time.Minute), 20)
	}
	if got := rootPaths(r); got != "/home/b,/home/a,/home" {
		t.Errorf("order = %v, want most recent first", got)
	}

	// Going back somewhere moves it, rather than listing it twice — and an
	// uncleaned spelling of the same place is the same place.
	r.Visit("/home/a/", now.Add(9*time.Minute), 20)
	if got := rootPaths(r); got != "/home/a,/home/b,/home" {
		t.Errorf("after revisiting /home/a: %v", got)
	}
}

func TestRootsCapDropsTheOldest(t *testing.T) {
	now := time.Now()
	r := NewRoots("/home")
	for i, p := range []string{"/a", "/b", "/c", "/d"} {
		r.Visit(p, now.Add(time.Duration(i)*time.Minute), 2)
	}
	if got := rootPaths(r); got != "/d,/c" {
		t.Errorf("capped list = %v, want the two newest", got)
	}
}

func TestRootsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	r := NewRoots("/proj")
	r.Visit("/proj/internal", now, 20)
	if err := r.Save(dir, 20, now, 0); err != nil {
		t.Fatal(err)
	}
	got := LoadRoots(dir, "/proj")
	if rootPaths(got) != "/proj/internal" || !got.Roots[0].At.Equal(now) {
		t.Fatalf("loaded %+v", got.Roots)
	}
	// Another launch root's history is another file.
	if other := LoadRoots(dir, "/elsewhere"); len(other.Roots) != 0 {
		t.Errorf("a different launch root loaded %+v", other.Roots)
	}
}

// A file that names a different launch root was renamed under us, or is a
// hash collision; either way it is not this tree's history.
func TestRootsLoadIgnoresAForeignFile(t *testing.T) {
	dir := t.TempDir()
	r := NewRoots("/someone/else")
	r.Visit("/someone/else/x", time.Now(), 20)
	if err := r.Overwrite(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, RootsFileName("/someone/else")), filepath.Join(dir, RootsFileName("/proj"))); err != nil {
		t.Fatal(err)
	}
	if got := LoadRoots(dir, "/proj"); len(got.Roots) != 0 {
		t.Errorf("loaded a foreign history: %+v", got.Roots)
	}
}

// Two sessions on one launch root each hold their own copy; saving must merge
// rather than let the second writer erase the first one's visits.
func TestRootsSaveMergesWithDisk(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	a := LoadRoots(dir, "/proj")
	b := LoadRoots(dir, "/proj")
	a.Visit("/proj/a", now, 20)
	if err := a.Save(dir, 20, now, 0); err != nil {
		t.Fatal(err)
	}
	b.Visit("/proj/b", now.Add(time.Minute), 20)
	if err := b.Save(dir, 20, now, 0); err != nil {
		t.Fatal(err)
	}
	if got := rootPaths(LoadRoots(dir, "/proj")); got != "/proj/b,/proj/a" {
		t.Errorf("merged = %v, want both sessions' visits", got)
	}
}

// Where both copies know a root, the later visit and the later sighting win
// independently.
func TestRootsSaveKeepsTheLaterOfEachTime(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Truncate(time.Second)

	disk := NewRoots("/proj")
	disk.Roots = []RecentRoot{{Path: "/proj/x", At: t0.Add(time.Hour), LastSeen: t0}}
	if err := disk.Overwrite(dir); err != nil {
		t.Fatal(err)
	}
	mem := NewRoots("/proj")
	mem.Roots = []RecentRoot{{Path: "/proj/x", At: t0, LastSeen: t0.Add(2 * time.Hour)}}
	if err := mem.Save(dir, 20, t0, 0); err != nil {
		t.Fatal(err)
	}
	got := LoadRoots(dir, "/proj").Roots
	if len(got) != 1 || !got[0].At.Equal(t0.Add(time.Hour)) || !got[0].LastSeen.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("merged entry = %+v, want the disk's visit and memory's sighting", got)
	}
}

// Expiry runs from the last sighting, not the last visit: a root you have not
// gone back to for months but that still exists is never at risk.
func TestRootsExpireRunsFromLastSeen(t *testing.T) {
	now := time.Now()
	r := NewRoots("/proj")
	r.Roots = []RecentRoot{
		{Path: "/proj/seen", At: now.Add(-90 * 24 * time.Hour), LastSeen: now},
		{Path: "/proj/gone", At: now.Add(-time.Hour), LastSeen: now.Add(-31 * 24 * time.Hour)},
	}
	if n := r.Expire(now, 30*24*time.Hour); n != 1 {
		t.Errorf("expired %d, want 1", n)
	}
	if got := rootPaths(r); got != "/proj/seen" {
		t.Errorf("left %v, want only the root still being seen", got)
	}
	// Zero retention turns expiry off rather than expiring everything.
	r.Roots = append(r.Roots, RecentRoot{Path: "/old", LastSeen: now.Add(-1000 * 24 * time.Hour)})
	if n := r.Expire(now, 0); n != 0 {
		t.Errorf("zero retention expired %d", n)
	}
}

// Forgetting must survive the write: Save would merge the root straight back
// in from disk, which is why Remove is paired with Overwrite.
func TestRootsRemoveThenOverwriteForgets(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	r := NewRoots("/proj")
	r.Visit("/proj/a", now, 20)
	r.Visit("/proj/b", now.Add(time.Second), 20)
	if err := r.Save(dir, 20, now, 0); err != nil {
		t.Fatal(err)
	}
	r = LoadRoots(dir, "/proj")
	if !r.Remove("/proj/a") {
		t.Fatal("Remove reported nothing removed")
	}
	if r.Remove("/proj/a") {
		t.Error("removing twice reported success")
	}
	if err := r.Overwrite(dir); err != nil {
		t.Fatal(err)
	}
	if got := rootPaths(LoadRoots(dir, "/proj")); got != "/proj/b" {
		t.Errorf("after forgetting: %v", got)
	}
}

// Sessions on one launch root save concurrently — that is what the merge is
// for — so no write may trample another's temporary file. With a shared name
// one session truncates it while another renames it into place: a rename that
// fails, or a half-written file LoadRoots throws away with the whole list.
func TestRootsConcurrentSavesLeaveAValidFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := NewRoots("/proj")
			r.Visit(fmt.Sprintf("/proj/d%02d", i), now.Add(time.Duration(i)*time.Second), 100)
			errs <- r.Save(dir, 100, now, 0)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent save: %v", err)
		}
	}
	if got := LoadRoots(dir, "/proj"); len(got.Roots) == 0 {
		t.Error("concurrent saves left a history that loads empty")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(leftovers) > 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}
