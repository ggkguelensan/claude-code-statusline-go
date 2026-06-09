package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// git runs `git -C cwd args...` with a short timeout and returns trimmed stdout,
// or "" on any error (matching the original script's best-effort behavior).
func git(cwd string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// git runs with -C inside cwd, which may be an attacker-controlled repo whose
	// local config can name programs git will exec (core.fsmonitor on `status`,
	// hooks, aliases). Neutralize those vectors and hand git an environment with
	// the Asana token stripped, so a hostile repo can neither run code with our
	// secrets present nor read the token out of git's environment.
	// GIT_OPTIONAL_LOCKS=0 keeps us from touching the index of a repo we inspect.
	full := append([]string{"-C", cwd, "-c", "core.fsmonitor=", "-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(scrubbedEnv(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// secretEnvKeys are env vars only the Asana fetch needs; every other subprocess
// (git, glab) runs without them so a repo run inside cannot leak the token.
var secretEnvKeys = map[string]bool{
	"ASANA_ACCESS_TOKEN": true,
	"ASANA_TOKEN":        true,
	"ASANA_PAT":          true,
}

// scrubbedEnv is os.Environ() with the Asana secrets removed — the environment
// handed to git and glab, neither of which needs an Asana credential.
func scrubbedEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		if i := strings.IndexByte(kv, '='); i >= 0 && secretEnvKeys[kv[:i]] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// RepoInfo holds the git facts a single directory contributes to the status line.
type RepoInfo struct {
	Root      string // repository top level
	Name      string // basename of Root
	Branch    string // branch name, or @<short-sha> when detached
	CommonDir string // .git common dir — equals GitDir except in linked worktrees
	GitDir    string // this checkout's .git dir
	IsWorktree bool  // true when GitDir != CommonDir (linked worktree)
	Dirty     bool   // uncommitted changes present
}

// repoInfo returns git facts for directory d, or nil when d is not in a repo.
func repoInfo(d string) *RepoInfo {
	out := git(d, "rev-parse", "--show-toplevel", "--git-dir", "--git-common-dir")
	lines := strings.Split(out, "\n")
	if len(lines) < 3 || lines[0] == "" {
		return nil
	}
	root := realpath(lines[0])
	gitDir := realpath(resolveRel(d, lines[1]))
	commonDir := realpath(resolveRel(d, lines[2]))

	branch := git(d, "branch", "--show-current")
	if branch == "" {
		branch = "@" + git(d, "rev-parse", "--short", "HEAD")
	}
	return &RepoInfo{
		Root:       root,
		Name:       filepath.Base(root),
		Branch:     branch,
		CommonDir:  commonDir,
		GitDir:     gitDir,
		IsWorktree: gitDir != commonDir,
		Dirty:      git(d, "status", "--porcelain") != "",
	}
}

// resolveRel joins base/p when p is relative, mirroring os.path.join(d, p).
func resolveRel(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// realpath resolves symlinks; falls back to a cleaned absolute path on error so
// a missing/odd path still yields a stable cache key.
func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// gitConfig reads a single git config value for directory d, or "".
func gitConfig(d, key string) string {
	return git(d, "config", "--get", key)
}

// homeDir is os.UserHomeDir with a $HOME fallback.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}
