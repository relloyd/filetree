package tree

import (
	"errors"
	"reflect"
	"testing"
)

// fakeLister serves listings from a map of dir path -> entries.
func fakeLister(m map[string][]Entry) Lister {
	return func(path string) ([]Entry, error) {
		if es, ok := m[path]; ok {
			return es, nil
		}
		return nil, errors.New("no such dir: " + path)
	}
}

func testFS() map[string][]Entry {
	return map[string][]Entry{
		"/root": {
			{Name: "zeta.go"},
			{Name: "cmd", IsDir: true},
			{Name: ".hidden"},
			{Name: "Alpha.md"},
			{Name: "internal", IsDir: true},
		},
		"/root/cmd": {{Name: "main.go"}},
		"/root/internal": {
			{Name: "app", IsDir: true},
		},
		"/root/internal/app": {{Name: "model.go"}},
	}
}

func names(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Node.Name
	}
	return out
}

func TestExpandFlattenSort(t *testing.T) {
	tr := New("/root", fakeLister(testFS()))
	if err := tr.Expand(tr.Root); err != nil {
		t.Fatal(err)
	}
	rows := tr.Flatten(nil)
	// Dirs first, then case-insensitive names.
	want := []string{"root", "cmd", "internal", ".hidden", "Alpha.md", "zeta.go"}
	if !reflect.DeepEqual(names(rows), want) {
		t.Errorf("rows = %v, want %v", names(rows), want)
	}

	// Depths: root 0, its children 1.
	if rows[0].Depth != 0 || rows[1].Depth != 1 {
		t.Errorf("depths = %d, %d", rows[0].Depth, rows[1].Depth)
	}

	// Filter hides dotfiles.
	rows = tr.Flatten(func(n *Node) bool { return n.Name[0] != '.' })
	want = []string{"root", "cmd", "internal", "Alpha.md", "zeta.go"}
	if !reflect.DeepEqual(names(rows), want) {
		t.Errorf("filtered rows = %v, want %v", names(rows), want)
	}
}

func TestCollapseRemembersDescendants(t *testing.T) {
	tr := New("/root", fakeLister(testFS()))
	tr.ExpandRel("internal/app")
	rows := tr.Flatten(nil)
	if got := names(rows); got[3] != "app" {
		t.Fatalf("rows = %v", got)
	}

	internal := tr.FindByPath("/root/internal")
	tr.Collapse(internal)
	if l := len(tr.Flatten(nil)); l != 6 {
		t.Fatalf("collapsed rows = %d, want 6", l)
	}
	// Re-expanding shows app still expanded.
	if err := tr.Expand(internal); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range tr.Flatten(nil) {
		if r.Node.Name == "model.go" {
			found = true
		}
	}
	if !found {
		t.Error("descendant expansion forgotten after collapse/expand")
	}
}

func TestExpandedRelsOrder(t *testing.T) {
	tr := New("/root", fakeLister(testFS()))
	tr.ExpandRel("internal/app")
	tr.ExpandRel("cmd")
	got := tr.ExpandedRels()
	want := []string{".", "cmd", "internal", "internal/app"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpandedRels = %v, want %v", got, want)
	}
}

func TestRefreshPreservesSubtrees(t *testing.T) {
	fs := testFS()
	tr := New("/root", fakeLister(fs))
	tr.ExpandRel("internal/app")
	app := tr.FindByPath("/root/internal/app")

	// A new file appears and one is renamed at the root.
	fs["/root"] = []Entry{
		{Name: "cmd", IsDir: true},
		{Name: "internal", IsDir: true},
		{Name: "new.txt"},
		{Name: "Alpha.md"},
	}
	if err := tr.Refresh(tr.Root); err != nil {
		t.Fatal(err)
	}
	if tr.FindByPath("/root/internal/app") != app {
		t.Error("refresh should reuse the existing internal/app node")
	}
	if tr.FindByPath("/root/new.txt") == nil {
		t.Error("new file missing after refresh")
	}
	if tr.FindByPath("/root/zeta.go") != nil {
		t.Error("deleted file still present after refresh")
	}
	if !tr.FindByPath("/root/internal").Expanded {
		t.Error("expansion lost on refresh")
	}
}

func TestExpandRelMissingSegmentIsIgnored(t *testing.T) {
	tr := New("/root", fakeLister(testFS()))
	tr.ExpandRel("gone/deeper") // must not panic or error
	if !tr.Root.Expanded {
		t.Error("root should still be expanded")
	}
}
