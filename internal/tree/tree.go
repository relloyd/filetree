// Package tree models the lazily-loaded directory tree: nodes are read from
// disk only when expanded, expansion state survives collapse (JetBrains
// style), and rendering works from a flattened list of visible rows.
package tree

import (
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one directory-listing result, produced by a Lister.
type Entry struct {
	Name          string
	IsDir         bool // for symlinks: whether the target is a directory
	IsSymlink     bool
	SymlinkTarget string
	Broken        bool // symlink whose target does not exist
}

// Lister reads the immediate children of an absolute directory path.
// Injected so the package stays filesystem-free and testable.
type Lister func(path string) ([]Entry, error)

type Node struct {
	Name          string
	Path          string // absolute
	IsDir         bool
	IsSymlink     bool
	SymlinkTarget string
	Broken        bool
	Expanded      bool
	Loaded        bool
	Children      []*Node
	Parent        *Node
}

// Row is one visible line of the rendered tree.
type Row struct {
	Node  *Node
	Depth int
}

type Tree struct {
	Root *Node
	list Lister
}

func New(rootPath string, list Lister) *Tree {
	rootPath = filepath.Clean(rootPath)
	return &Tree{
		Root: &Node{Name: filepath.Base(rootPath), Path: rootPath, IsDir: true},
		list: list,
	}
}

func (t *Tree) Load(n *Node) error {
	entries, err := t.list(n.Path)
	if err != nil {
		return err
	}
	n.Children = make([]*Node, 0, len(entries))
	for _, e := range entries {
		n.Children = append(n.Children, &Node{
			Name:          e.Name,
			Path:          filepath.Join(n.Path, e.Name),
			IsDir:         e.IsDir,
			IsSymlink:     e.IsSymlink,
			SymlinkTarget: e.SymlinkTarget,
			Broken:        e.Broken,
			Parent:        n,
		})
	}
	sortNodes(n.Children)
	n.Loaded = true
	return nil
}

func (t *Tree) Expand(n *Node) error {
	if !n.IsDir {
		return nil
	}
	if !n.Loaded {
		if err := t.Load(n); err != nil {
			return err
		}
	}
	n.Expanded = true
	return nil
}

func (t *Tree) Collapse(n *Node) {
	n.Expanded = false
}

// Refresh re-lists a loaded directory, reusing existing child nodes (and
// therefore their expansion state and subtrees) where names still match.
func (t *Tree) Refresh(n *Node) error {
	if !n.IsDir || !n.Loaded {
		return nil
	}
	entries, err := t.list(n.Path)
	if err != nil {
		// Directory vanished; the parent's refresh will prune this node.
		return err
	}
	old := make(map[string]*Node, len(n.Children))
	for _, c := range n.Children {
		old[c.Name] = c
	}
	next := make([]*Node, 0, len(entries))
	for _, e := range entries {
		if c, ok := old[e.Name]; ok && c.IsDir == e.IsDir {
			c.IsSymlink = e.IsSymlink
			c.SymlinkTarget = e.SymlinkTarget
			c.Broken = e.Broken
			next = append(next, c)
			continue
		}
		next = append(next, &Node{
			Name:          e.Name,
			Path:          filepath.Join(n.Path, e.Name),
			IsDir:         e.IsDir,
			IsSymlink:     e.IsSymlink,
			SymlinkTarget: e.SymlinkTarget,
			Broken:        e.Broken,
			Parent:        n,
		})
	}
	sortNodes(next)
	n.Children = next
	return nil
}

// Flatten returns the visible rows: the root, then children of every
// expanded directory that pass the visible filter.
func (t *Tree) Flatten(visible func(*Node) bool) []Row {
	rows := []Row{{Node: t.Root, Depth: 0}}
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		if !n.Expanded {
			return
		}
		for _, c := range n.Children {
			if visible != nil && !visible(c) {
				continue
			}
			rows = append(rows, Row{Node: c, Depth: depth})
			if c.IsDir {
				walk(c, depth+1)
			}
		}
	}
	walk(t.Root, 1)
	return rows
}

// Rel returns path relative to the tree root ("." for the root itself),
// always slash-separated.
func (t *Tree) Rel(path string) string {
	r, err := filepath.Rel(t.Root.Path, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}

// FindByPath walks already-loaded nodes to the node for an absolute path.
func (t *Tree) FindByPath(path string) *Node {
	rel := t.Rel(path)
	if rel == "." {
		return t.Root
	}
	n := t.Root
segments:
	for _, seg := range strings.Split(rel, "/") {
		for _, c := range n.Children {
			if c.Name == seg {
				n = c
				continue segments
			}
		}
		return nil
	}
	return n
}

// ExpandRel expands the directory at a root-relative path, loading and
// expanding ancestors along the way. Missing segments are ignored (the
// remembered dir may have been deleted).
func (t *Tree) ExpandRel(rel string) {
	if err := t.Expand(t.Root); err != nil {
		return
	}
	if rel == "." {
		return
	}
	n := t.Root
segments:
	for _, seg := range strings.Split(rel, "/") {
		for _, c := range n.Children {
			if c.Name == seg && c.IsDir {
				if err := t.Expand(c); err != nil {
					return
				}
				n = c
				continue segments
			}
		}
		return
	}
}

// ExpandedRels returns every expanded directory as a root-relative path,
// parents before children, including dirs hidden under collapsed ancestors.
func (t *Tree) ExpandedRels() []string {
	var rels []string
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Expanded {
			rels = append(rels, t.Rel(n.Path))
		}
		for _, c := range n.Children {
			if c.IsDir {
				walk(c)
			}
		}
	}
	walk(t.Root)
	sort.Slice(rels, func(i, j int) bool {
		ci, cj := strings.Count(rels[i], "/"), strings.Count(rels[j], "/")
		if ci != cj {
			return ci < cj
		}
		return rels[i] < rels[j]
	})
	return rels
}

// CollapseAll collapses everything below the root and forgets remembered
// expansion of descendants.
func (t *Tree) CollapseAll() {
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			if c.IsDir {
				c.Expanded = false
				walk(c)
			}
		}
	}
	walk(t.Root)
}

func sortNodes(ns []*Node) {
	sort.SliceStable(ns, func(i, j int) bool {
		a, b := ns[i], ns[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		al, bl := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if al != bl {
			return al < bl
		}
		return a.Name < b.Name
	})
}
