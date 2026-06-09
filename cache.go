package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// cacheTTL is how long MR/Asana data is reused before a background refresh is
// triggered. Both sources change on the scale of minutes, so a short TTL keeps
// the foreground render instant (local git only) without going stale.
const cacheTTL = 90 * time.Second

// Cache is the on-disk per-(repo,branch) snapshot of the remote segments.
type Cache struct {
	FetchedAt int64      `json:"fetched_at"` // unix seconds
	Branch    string     `json:"branch"`
	MR        *MR        `json:"mr,omitempty"`
	Asana     *AsanaTask `json:"asana,omitempty"`
}

func (c *Cache) stale(now time.Time) bool {
	return c == nil || now.Unix()-c.FetchedAt >= int64(cacheTTL.Seconds())
}

// cacheDir is $XDG_CACHE_HOME/cc-statusline or ~/.cache/cc-statusline.
func cacheDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(homeDir(), ".cache")
	}
	return filepath.Join(base, "cc-statusline")
}

// cacheKey is a stable id for a (repo, branch) pair. Using the git common dir
// means every linked worktree of the same repo shares the repo identity while
// different branches still get distinct cache files.
func cacheKey(commonDir, branch string) string {
	h := sha256.Sum256([]byte(commonDir + "\x00" + branch))
	return hex.EncodeToString(h[:])[:16]
}

func cachePath(key string) string  { return filepath.Join(cacheDir(), key+".json") }
func lockPath(key string) string   { return filepath.Join(cacheDir(), key+".lock") }

// readCache returns the cached snapshot, or nil when missing/unreadable.
func readCache(key string) *Cache {
	b, err := os.ReadFile(cachePath(key))
	if err != nil {
		return nil
	}
	var c Cache
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &c
}

// writeCache writes the snapshot atomically (temp file + rename).
func writeCache(key string, c *Cache) error {
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, cachePath(key))
}

// spawnRefresh launches a detached `self --refresh` so the foreground render
// never blocks on the network. Best-effort: errors are ignored.
//
// A status line is re-invoked on every redraw, and a single refresh takes up to
// ~10s, during which the cache stays stale — so without a guard every redraw in
// that window would fork a fresh subprocess that does nothing but fail to take
// the lock and exit. We collapse that storm by probing the same lock in the
// foreground first: if a refresh already holds it, skip the spawn entirely.
func spawnRefresh(key, dir, branch string) {
	if !refreshSlotFree(key) {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "--refresh", "--key", key, "--dir", dir, "--branch", branch)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from this process group
	_ = cmd.Start()                                       // do not Wait
}

// refreshSlotFree reports whether no refresh currently holds the lock. It takes
// the lock non-blocking and immediately releases it, so the spawned child can
// re-acquire it. The tiny release→child-acquire gap can at worst let through one
// extra spawn, vs. the dozens the guard prevents.
func refreshSlotFree(key string) bool {
	if err := os.MkdirAll(cacheDir(), 0o700); err != nil {
		return true // can't probe — fall through to spawning
	}
	lf, err := os.OpenFile(lockPath(key), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return true
	}
	defer lf.Close()
	if syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return false // a refresh is in flight
	}
	syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	return true
}

// runRefresh is the `--refresh` entry point: it fetches MR + Asana concurrently
// and writes the cache. A non-blocking file lock collapses concurrent refreshes.
func runRefresh(key, dir, branch string) {
	if key == "" {
		if r := repoInfo(dir); r != nil {
			key = cacheKey(r.CommonDir, branch)
		}
	}
	if key == "" {
		return
	}
	if err := os.MkdirAll(cacheDir(), 0o700); err != nil {
		return
	}

	lf, err := os.OpenFile(lockPath(key), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer lf.Close()
	if syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return // another refresh is in flight
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	// Another refresh may have just finished while we waited for the lock.
	if c := readCache(key); !c.stale(time.Now()) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), glabTimeout+2*time.Second)
	defer cancel()

	var (
		wg    sync.WaitGroup
		mr    *MR
		asana *AsanaTask
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		mctx, mcancel := context.WithTimeout(ctx, glabTimeout)
		defer mcancel()
		mr = fetchMR(mctx, dir, branch)
	}()
	go func() {
		defer wg.Done()
		actx, acancel := context.WithTimeout(ctx, glabTimeout)
		defer acancel()
		asana = fetchAsana(actx, dir, branch)
	}()
	wg.Wait()

	_ = writeCache(key, &Cache{
		FetchedAt: time.Now().Unix(),
		Branch:    branch,
		MR:        mr,
		Asana:     asana,
	})
}
