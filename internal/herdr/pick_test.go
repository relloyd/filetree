package herdr

import "testing"

// The places used throughout: a main repo, a linked worktree of the same
// project living outside it, and a directory belonging to no repository at all.
const (
	repo     = "/Users/r/filetree"
	worktree = "/Users/r/.filetree/worktrees/filetree/feature-x"
	loose    = "/Users/r/Downloads"
)

// inRepo and inLoose name the two kinds of scope so the table reads as what it
// is testing rather than as a pair of struct literals.
func inRepo(root string) Scope { return ScopeFor("", root) }
func inLoose(dir string) Scope { return ScopeFor(dir, "") }

func TestPick(t *testing.T) {
	tests := []struct {
		name   string
		shells []Shell
		dir    string
		scope  Scope
		tab    string
		want   string // pane id, "" for no match
	}{
		{
			name:   "no shells at all is the create path, not an error",
			shells: nil,
			dir:    repo + "/internal/app",
			scope:  inRepo(repo),
			want:   "",
		},
		{
			name: "the only shell in the checkout wins however far away it is",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "w1:p1",
		},
		{
			name: "a shell in the selection's own directory beats one at the root",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
				{PaneID: "w1:p2", TabID: "w1:t2", Dir: repo + "/internal/app", Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "w1:p2",
		},
		{
			name: "a directory holding the selection beats a sibling that shares as much",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo + "/internal/appserver", Checkout: repo},
				{PaneID: "w1:p2", TabID: "w1:t2", Dir: repo + "/internal", Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "w1:p2",
		},
		{
			name: "holding the selection beats being nearer to it but off to one side",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo + "/internal/appserver/deep", Checkout: repo},
				{PaneID: "w1:p2", TabID: "w1:t2", Dir: repo, Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "w1:p2",
		},
		{
			name: "among directories that all hold the selection, the deepest wins",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
				{PaneID: "w1:p2", TabID: "w1:t2", Dir: repo + "/internal", Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "w1:p2",
		},
		{
			name: "with nothing holding the selection, the nearest dead end still answers",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo + "/cmd", Checkout: repo},
				{PaneID: "w1:p2", TabID: "w1:t2", Dir: repo + "/internal/appserver", Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "w1:p2",
		},
		{
			name: "with equal nearness the tree's own tab wins",
			shells: []Shell{
				{PaneID: "w2:p9", TabID: "w2:t9", Dir: repo, Checkout: repo},
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			tab:   "w1:t1",
			want:  "w1:p1",
		},
		{
			name: "nearness still beats the tree's own tab",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
				{PaneID: "w2:p9", TabID: "w2:t9", Dir: repo + "/internal/app", Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			tab:   "w1:t1",
			want:  "w2:p9",
		},
		{
			name: "a dead heat resolves the same way every press",
			shells: []Shell{
				{PaneID: "w1:p7", TabID: "w1:t7", Dir: repo, Checkout: repo},
				{PaneID: "w1:p3", TabID: "w1:t3", Dir: repo, Checkout: repo},
			},
			dir:   repo,
			scope: inRepo(repo),
			want:  "w1:p3",
		},
		{
			name: "a shell in a worktree never answers for the main checkout",
			shells: []Shell{
				{PaneID: "w2:p1", TabID: "w2:t1", Dir: worktree, Checkout: worktree},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "",
		},
		{
			name: "and a shell in the main checkout never answers for a worktree",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
			},
			dir:   worktree + "/internal/app",
			scope: inRepo(worktree),
			want:  "",
		},
		{
			name: "the right checkout wins even when the wrong one sits nearer the cursor",
			shells: []Shell{
				{PaneID: "w2:p1", TabID: "w2:t1", Dir: worktree + "/internal/app", Checkout: worktree},
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "w1:p1",
		},
		{
			name: "a shell whose checkout could not be resolved is not a candidate in a repo",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: "/tmp/scratch"},
			},
			dir:   repo,
			scope: inRepo(repo),
			want:  "",
		},
		{
			name: "paths needing cleaning still compare equal",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo + "/internal/../internal/app", Checkout: repo + "/"},
			},
			dir:   repo + "/internal/app",
			scope: inRepo(repo),
			want:  "w1:p1",
		},

		// Outside a checkout the selection's own directory is the boundary.
		{
			name: "a loose directory finds a shell sitting in it",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: loose},
			},
			dir:   loose,
			scope: inLoose(loose),
			want:  "w1:p1",
		},
		{
			name: "a shell above the selection answers, which is what stops a second workspace per subdirectory",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: loose},
			},
			dir:   loose + "/stuff/deep",
			scope: inLoose(loose + "/stuff/deep"),
			want:  "w1:p1",
		},
		{
			name: "a shell below the selection answers too",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: loose + "/stuff"},
			},
			dir:   loose,
			scope: inLoose(loose),
			want:  "w1:p1",
		},
		{
			name: "but one off to the side does not",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: "/Users/r/Music"},
			},
			dir:   loose,
			scope: inLoose(loose),
			want:  "",
		},
		{
			name: "a shell inside a repository below belongs to that repository, not to the directory holding it",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: loose + "/someproject", Checkout: loose + "/someproject"},
			},
			dir:   loose,
			scope: inLoose(loose),
			want:  "",
		},
		{
			name: "the nearer of two loose shells wins",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: "/Users/r"},
				{PaneID: "w1:p2", TabID: "w1:t2", Dir: loose},
			},
			dir:   loose + "/stuff",
			scope: inLoose(loose + "/stuff"),
			want:  "w1:p2",
		},
		{
			name: "a repo shell never answers for a loose directory that contains the repo",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
			},
			dir:   "/Users/r",
			scope: inLoose("/Users/r"),
			want:  "",
		},
		{
			name: "an empty scope matches nothing at all",
			shells: []Shell{
				{PaneID: "w1:p1", TabID: "w1:t1", Dir: repo, Checkout: repo},
			},
			dir:   repo,
			scope: Scope{},
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Pick(tc.shells, tc.dir, tc.scope, tc.tab)
			if tc.want == "" {
				if ok {
					t.Fatalf("Pick matched %q, want no match", got.PaneID)
				}
				return
			}
			if !ok {
				t.Fatalf("Pick found nothing, want %q", tc.want)
			}
			if got.PaneID != tc.want {
				t.Errorf("Pick = %q, want %q", got.PaneID, tc.want)
			}
		})
	}
}

func TestScopeFor(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		root     string
		wantRoot string
		wantRepo bool
		wantName string
	}{
		{"in a checkout, the checkout is the boundary", repo + "/internal", repo, repo, true, "filetree"},
		{"a worktree is its own boundary", worktree + "/internal", worktree, worktree, true, "feature-x"},
		{"outside one, the directory is", loose + "/stuff", "", loose + "/stuff", false, "stuff"},
		{"nothing at all", "", "", "", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := ScopeFor(tc.dir, tc.root)
			if s.Root != tc.wantRoot || s.Repo != tc.wantRepo {
				t.Errorf("ScopeFor = %+v, want {Root:%q Repo:%v}", s, tc.wantRoot, tc.wantRepo)
			}
			if got := s.Name(); got != tc.wantName {
				t.Errorf("Name() = %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestSharedSegments(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"/a/b/c", "/a/b/c", 3},
		{"/a/b", "/a/b/c", 2},
		{"/a/b/c", "/a/b", 2},
		{"/a/proj", "/a/proj2", 1}, // the case a character prefix would get wrong
		{"/a", "/b", 0},
		{"", "/a/b", 0},
		{"/a/b", "", 0},
	}
	for _, tc := range tests {
		if got := sharedSegments(tc.a, tc.b); got != tc.want {
			t.Errorf("sharedSegments(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestWithin(t *testing.T) {
	tests := []struct {
		root, p string
		want    bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a", false},
		{"/a/proj", "/a/proj2", false}, // not a character prefix
		{"", "/a", false},
		{"/a", "", false},
	}
	for _, tc := range tests {
		if got := within(tc.root, tc.p); got != tc.want {
			t.Errorf("within(%q, %q) = %v, want %v", tc.root, tc.p, got, tc.want)
		}
	}
}

// AtPrompt is the filter that keeps this key away from a shell that is busy, so
// its two ids are worth pinning down directly.
func TestAtPrompt(t *testing.T) {
	tests := []struct {
		name string
		info ProcessInfo
		want bool
	}{
		{"idle shell owns the terminal", ProcessInfo{ShellPID: 90145, ForegroundPGID: 90145}, true},
		{"something is running on top of it", ProcessInfo{ShellPID: 38389, ForegroundPGID: 38686}, false},
		{"herdr could not inspect the pane", ProcessInfo{}, false},
		{"no shell but a foreground group", ProcessInfo{ForegroundPGID: 900}, false},
	}
	for _, tc := range tests {
		if got := tc.info.AtPrompt(); got != tc.want {
			t.Errorf("%s: AtPrompt() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Cwd prefers where the shell actually is over where it started, which is what
// makes a "cd" inside a pane visible to the matcher.
func TestPaneCwd(t *testing.T) {
	tests := []struct {
		name string
		pane Pane
		want string
	}{
		{"foreground wins", Pane{Dir: repo, ForegroundDir: repo + "/internal"}, repo + "/internal"},
		{"falls back to the start directory", Pane{Dir: repo}, repo},
		{"nothing known", Pane{}, ""},
	}
	for _, tc := range tests {
		if got := tc.pane.Cwd(); got != tc.want {
			t.Errorf("%s: Cwd() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
