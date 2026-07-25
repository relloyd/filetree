package gitx

import (
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

// RemoteURL returns the URL of the repo's origin remote.
func RemoteURL(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("no origin remote configured")
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadRef returns the ref to embed in web links: the HEAD commit hash
// (permanent), or the current branch name when useBranch is set — falling
// back to the hash on a detached HEAD.
func HeadRef(root string, useBranch bool) (string, error) {
	if useBranch {
		out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err == nil {
			if b := strings.TrimSpace(string(out)); b != "" && b != "HEAD" {
				return b, nil
			}
		}
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("repository has no commits")
	}
	return strings.TrimSpace(string(out)), nil
}

// scpLike matches scp-style remotes: [user@]host:path
var scpLike = regexp.MustCompile(`^(?:[^@/:]+@)?([A-Za-z0-9._-]+):(.+)$`)

// WebURL converts a git remote URL to the https base URL of its web host.
// Handles scp-like (git@host:user/repo.git), ssh://, git://, and http(s)://
// forms; credentials and ssh ports are dropped, ".git" is stripped.
func WebURL(remote string) (string, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", false
	}
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" {
			return "", false
		}
		host := u.Host // http(s): keep an explicit port (enterprise setups)
		switch u.Scheme {
		case "http", "https":
		case "ssh", "git":
			host = u.Hostname() // the web UI is not on the ssh port
		default:
			return "", false
		}
		path := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
		if path == "" || path == "/" {
			return "", false
		}
		return "https://" + host + path, true
	}
	if m := scpLike.FindStringSubmatch(remote); m != nil {
		path := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSuffix(m[2], "/"), ".git"), "/")
		if path == "" {
			return "", false
		}
		return "https://" + m[1] + "/" + path, true
	}
	return "", false
}

// FileURL builds a GitHub-style URL for a repo-relative path: /blob/ for
// files, /tree/ for directories, the bare tree for the repo root. Path
// segments are URL-escaped.
func FileURL(base, ref, relPath string, isDir bool) string {
	if relPath == "" || relPath == "." {
		return base + "/tree/" + ref
	}
	segs := strings.Split(relPath, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	kind := "blob"
	if isDir {
		kind = "tree"
	}
	return base + "/" + kind + "/" + ref + "/" + strings.Join(segs, "/")
}
