// Command claude-code-statusline renders the Claude Code status line.
//
//	O 4.8 1M 🚀xh | 🌿 branch* | FTP-3853 Backlog | MR!1297 ✓ | ctx 31% | +156/-23 · 12m | 5h 23% · 7d 41%
//
// It reads the status-line JSON on stdin (see
// https://code.claude.com/docs/en/statusline) and writes one line to stdout.
// The GitLab MR and Asana task being worked on are fetched out-of-band: the
// foreground render only touches local git and a small cache, while a detached
// `--refresh` subprocess keeps that cache warm. Optional segments are omitted
// when their data is absent.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	var (
		refresh = flag.Bool("refresh", false, "background mode: fetch MR/Asana and update the cache")
		key     = flag.String("key", "", "cache key (refresh mode)")
		dir     = flag.String("dir", "", "repo directory (refresh mode)")
		branch  = flag.String("branch", "", "branch name (refresh mode)")
	)
	flag.Parse()

	if *refresh {
		runRefresh(*key, *dir, *branch)
		return
	}

	in := readInput()
	fmt.Println(render(in, float64(time.Now().Unix())))
}

// readInput decodes the status-line JSON from stdin, tolerating malformed or
// empty input by returning a zero-value Input (the line still renders the model
// placeholder rather than crashing the status bar).
func readInput() *Input {
	var in Input
	b, _ := io.ReadAll(os.Stdin)
	_ = json.Unmarshal(b, &in)
	return &in
}

// render assembles the status line from the input plus cached remote data.
func render(in *Input, now float64) string {
	gitSeg, repos := segGit(in)

	var mrSeg, asanaSeg string
	if len(repos) > 0 {
		primary := repos[0]
		key := cacheKey(primary.CommonDir, primary.Branch)
		c := readCache(key)
		if c != nil {
			mrSeg = segMR(c.MR)
			asanaSeg = segAsana(c.Asana)
		}
		if c.stale(time.Unix(int64(now), 0)) {
			spawnRefresh(key, primary.Root, primary.Branch)
		}
	}

	segments := []string{
		segModel(in),
		gitSeg,
		asanaSeg,
		mrSeg,
		segPR(in),
		segContext(in),
		segChanges(in),
		segRateLimits(in, now),
	}

	nonEmpty := segments[:0]
	for _, s := range segments {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	return strings.Join(nonEmpty, sep)
}
