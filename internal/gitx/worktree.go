package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// IsLinkedWorktree reports whether dir is the root of a linked worktree.
// Those have a .git FILE pointing into the main repo's worktrees admin dir;
// submodules also use a .git file, but their gitdir lacks "/worktrees/".
func IsLinkedWorktree(dir string) bool {
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "gitdir:") && strings.Contains(s, "/worktrees/")
}

// MainRepoOf returns the main repository a linked worktree belongs to. The
// owning repo is not the worktree's parent directory, so ask git: the common
// git dir is <main repo>/.git.
func MainRepoOf(worktree string) (string, error) {
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("not a git worktree: %s", worktree)
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return "", fmt.Errorf("cannot locate the main repository of %s", worktree)
	}
	return filepath.Dir(common), nil
}

// WorktreeAdd checks ref out into dest, creating ref as a new branch off
// HEAD when newBranch is set.
func WorktreeAdd(repoRoot, dest, ref string, newBranch bool) error {
	args := []string{"-C", repoRoot, "worktree", "add"}
	if newBranch {
		args = append(args, "-b", ref, dest)
	} else {
		args = append(args, dest, ref)
	}
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s", gitErr(out, err))
	}
	return nil
}

// WorktreeRemove deletes a linked worktree; force discards local changes.
func WorktreeRemove(repoRoot, dest string, force bool) error {
	args := []string{"-C", repoRoot, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, dest)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s", gitErr(out, err))
	}
	// Best effort: the removal already succeeded, this only tidies admin files.
	_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
	return nil
}

// IsDirtyWorktreeError reports whether a WorktreeRemove failure was git
// refusing to throw away local changes — the case a forced retry fixes.
func IsDirtyWorktreeError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "modified or untracked") || strings.Contains(s, "use --force")
}

// FetchPR fetches pull request n from origin into a local pr-<n> branch and
// returns that branch name.
func FetchPR(repoRoot string, n int) (string, error) {
	branch := fmt.Sprintf("pr-%d", n)
	spec := fmt.Sprintf("pull/%d/head:%s", n, branch)
	if out, err := exec.Command("git", "-C", repoRoot, "fetch", "origin", spec).CombinedOutput(); err != nil {
		// A force-update (+refspec) of a branch that is checked out in a
		// worktree fails, so an existing pr-<n> branch counts as success.
		if HasRef(repoRoot, branch) {
			return branch, nil
		}
		return "", fmt.Errorf("git fetch pull/%d/head: %s", n, gitErr(out, err))
	}
	return branch, nil
}

// FetchBranch fetches one branch from origin into refs/heads, for branches
// git could not resolve locally. The explicit refspec matters: a plain
// `git fetch origin <branch>` only writes FETCH_HEAD, leaving nothing for
// `git worktree add` to check out.
func FetchBranch(repoRoot, branch string) error {
	spec := branch + ":" + branch
	if out, err := exec.Command("git", "-C", repoRoot, "fetch", "origin", spec).CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch origin %s: %s", branch, gitErr(out, err))
	}
	return nil
}

// HasRef reports whether a local branch exists in the repo.
func HasRef(repoRoot, ref string) bool {
	return exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+ref).Run() == nil
}

// ParseWorktreeInput classifies what was typed at the worktree prompt: a
// pull request number ("123", "#123", "pr/123", "PR123", "pr-123") or, for
// anything else, a branch name.
func ParseWorktreeInput(s string) (pr int, branch string) {
	s = strings.TrimSpace(s)
	digits := strings.TrimPrefix(s, "#")
	if len(digits) >= 2 && strings.EqualFold(digits[:2], "pr") {
		digits = strings.TrimLeft(digits[2:], "#/-: ")
	}
	if digits != "" && strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		if n, err := strconv.Atoi(digits); err == nil && n > 0 {
			return n, ""
		}
	}
	return 0, s
}

// WorktreeDirName is the directory a worktree gets under
// <worktrees dir>/<repo>/: pr-<n> for pull requests, otherwise the branch
// with separators flattened, so the layout stays one level deep (and a
// branch name can never escape the worktrees directory). Returns "" when
// nothing usable is left.
func WorktreeDirName(input string) string {
	if pr, _ := ParseWorktreeInput(input); pr > 0 {
		return fmt.Sprintf("pr-%d", pr)
	}
	name := strings.TrimSpace(input)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, `\`, "-")
	if strings.Trim(name, ".") == "" {
		return "" // "." / ".." would point outside the layout
	}
	return name
}

// gitErr renders a failed git invocation for the status bar: its own output
// when it said something, otherwise the exec error.
func gitErr(out []byte, err error) string {
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	return err.Error()
}
