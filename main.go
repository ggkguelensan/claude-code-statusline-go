// Command claude-code-statusline renders the Claude Code status line.
//
//	O 4.8 1M 🚀xh | 🌿 branch* | FTP-3853 Backlog | MR!1297 ✓ | ctx 31% | +156/-23 · 12m | 5h 23% · 7d 41%
//
// It reads the status-line JSON on stdin (see
// https://code.claude.com/docs/en/statusline) and writes the line to stdout —
// usually one row, but in a terminal too narrow to hold it (width taken from
// $COLUMNS) the branch and session segments wrap onto a second row.
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
	"strconv"
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
	fmt.Println(render(in, float64(time.Now().Unix()), terminalCols()))
}

// terminalCols reads the terminal width from the COLUMNS env var, which Claude
// Code sets before invoking the status line command (v2.1.153+). It returns 0
// when the value is missing or unparseable, in which case render keeps the
// whole status line on a single row.
func terminalCols() int {
	n, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// readInput decodes the status-line JSON from stdin, tolerating malformed or
// empty input by returning a zero-value Input (the line still renders the model
// placeholder rather than crashing the status bar).
func readInput() *Input {
	var in Input
	// Cap stdin: a valid status JSON is well under 1 MiB, and the parent could in
	// principle stream unboundedly. Truncation only ever yields a zero-value Input.
	b, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	_ = json.Unmarshal(b, &in)
	return &in
}

// render assembles the status line from the input plus cached remote data. cols
// is the terminal width (0 = unknown); when the line would overflow it the
// branch and session segments wrap to a second row (see assemble).
func render(in *Input, now float64, cols int) string {
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

	return assemble(
		segModel(in), gitSeg, asanaSeg, mrSeg, segPR(in),
		segContext(in), segChanges(in), segRateLimits(in, now), cols,
	)
}
