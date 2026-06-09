package main

import (
	"regexp"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips ANSI color codes so assertions read against visible text.
func plain(s string) string { return ansi.ReplaceAllString(s, "") }

func TestShortModel(t *testing.T) {
	cases := map[string]string{
		"Opus 4.8 (1M context)": "O 4.8 1M",
		"Sonnet 4.6":            "S 4.6",
		"Haiku 4.5 (200K context)": "H 4.5 200K",
		"":                      "",
	}
	for in, want := range cases {
		if got := shortModel(in); got != want {
			t.Errorf("shortModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSegContext(t *testing.T) {
	cases := []struct {
		name   string
		in     Input
		want   string // ANSI-stripped
	}{
		{"percentage fallback", mkCtx(1_000_000, 7, nil), "ctx 70.0k (7%)"},
		{"explicit usage sums tokens", mkCtx(0, 9, &usage{Input: 50000, CacheRead: 42000}), "ctx 92.0k (9%)"},
		{"millions", mkCtx(0, 50, &usage{Input: 1_200_000}), "ctx 1.20M (50%)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := plain(segContext(&c.in)); got != c.want {
				t.Errorf("segContext = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSegContextColors(t *testing.T) {
	if got := segContext(ptr(mkCtx(0, 0, &usage{Input: 79_000}))); !strings.Contains(got, grn) {
		t.Errorf("79k should be green: %q", got)
	}
	if got := segContext(ptr(mkCtx(0, 0, &usage{Input: 85_000}))); !strings.Contains(got, ylw) {
		t.Errorf("85k should be yellow: %q", got)
	}
	if got := segContext(ptr(mkCtx(0, 0, &usage{Input: 120_000}))); !strings.Contains(got, red) {
		t.Errorf("120k should be red: %q", got)
	}
}

func TestSegChanges(t *testing.T) {
	in := Input{}
	in.Cost = &CostInfo{TotalCostUSD: 2.17, TotalLinesAdded: 194, TotalLinesRemoved: 77, TotalDurationMS: 4_140_000}
	// dollar cost is intentionally dropped; only +/- lines and duration remain.
	if got, want := plain(segChanges(&in)), "+194/-77 · 1h9m"; got != want {
		t.Errorf("segChanges = %q, want %q", got, want)
	}
	in.Cost.TotalDurationMS = 300_000
	if got, want := plain(segChanges(&in)), "+194/-77 · 5m"; got != want {
		t.Errorf("segChanges (minutes) = %q, want %q", got, want)
	}
	if got := segChanges(&in); strings.Contains(got, "$") {
		t.Errorf("segChanges must not contain a dollar cost: %q", got)
	}
}

func TestSegModelEffort(t *testing.T) {
	cases := map[string]string{
		"low":     "O 4.8 1M 🌱lo",
		"medium":  "O 4.8 1M ⚡med",
		"high":    "O 4.8 1M 🔥hi",
		"xhigh":   "O 4.8 1M 🚀xh",
		"max":     "O 4.8 1M 💥max",
		"unknown": "O 4.8 1M ⚡unknown", // unrecognised level keeps the bolt
	}
	for level, want := range cases {
		in := &Input{}
		in.Model.DisplayName = "Opus 4.8 (1M context)"
		in.Effort = &EffortInfo{Level: level}
		if got := plain(segModel(in)); got != want {
			t.Errorf("segModel(effort=%q) = %q, want %q", level, got, want)
		}
	}
}

func TestSegRateLimits(t *testing.T) {
	now := 1_000_000.0
	in := Input{}
	five := 5400.0  // 1.5h
	seven := 86400.0 // 1.0d
	r5 := now + five
	r7 := now + seven
	in.RateLimits = &RateLimits{
		FiveHour: &RLWindow{ResetsAt: &r5, UsedPercentage: 22},
		SevenDay: &RLWindow{ResetsAt: &r7, UsedPercentage: 91},
	}
	got := plain(segRateLimits(&in, now))
	if want := "1.5h 22% · 1.0d 91%"; got != want {
		t.Errorf("segRateLimits = %q, want %q", got, want)
	}
	// 91% used must color the countdown red.
	if !strings.Contains(segRateLimits(&in, now), red) {
		t.Error("≥90%% used should be red")
	}
}

func TestSegMR(t *testing.T) {
	cases := []struct {
		name string
		mr   *MR
		want string
	}{
		{"nil", nil, ""},
		{"green pipeline", &MR{IID: 1297, PipelineStatus: "success", BlockingDiscussionsResolved: true}, "MR!1297 ✓"},
		{"draft running", &MR{IID: 1297, Draft: true, PipelineStatus: "running", BlockingDiscussionsResolved: true}, "MR!1297 📝 ●"},
		{"canceling is red ✗", &MR{IID: 1297, PipelineStatus: "canceling", BlockingDiscussionsResolved: true}, "MR!1297 ✗"},
		{"conflicts beat pipeline", &MR{IID: 1297, HasConflicts: true, PipelineStatus: "success", BlockingDiscussionsResolved: true}, "MR!1297 ❗"},
		{"unresolved discussion", &MR{IID: 1297, PipelineStatus: "success", BlockingDiscussionsResolved: false}, "MR!1297 ✓ 💬"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := plain(segMR(c.mr)); got != c.want {
				t.Errorf("segMR = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSegAsana(t *testing.T) {
	cases := []struct {
		name string
		t    *AsanaTask
		want string
	}{
		{"nil", nil, ""},
		{"ftp + section", &AsanaTask{GID: "1", FTP: "FTP-3853", Section: "Backlog"}, "FTP-3853 Backlog"},
		{"completed", &AsanaTask{GID: "1", FTP: "FTP-3853", Section: "Done", Completed: true}, "✓ FTP-3853 Done"},
		{"no ftp falls back to name", &AsanaTask{GID: "1", Name: "Some very long task title that exceeds the limit", Section: "Doing"}, "Some very long task tit… Doing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := plain(segAsana(c.t)); got != c.want {
				t.Errorf("segAsana = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveTicketID(t *testing.T) {
	// branch-name extraction (no git config available for a bare string)
	if got := resolveTicketID("/nonexistent", "feature/FTP-3853-foo", "FTP"); got != "FTP-3853" {
		t.Errorf("branch regex = %q, want FTP-3853", got)
	}
	if got := resolveTicketID("/nonexistent", "ftp-42-lower", "FTP"); got != "FTP-42" {
		t.Errorf("case-insensitive uppercased = %q, want FTP-42", got)
	}
	// underscore separators must not block the match (Go's \b treats _ as a word char)
	if got := resolveTicketID("/nonexistent", "e2e_FTP-3853_toolkit", "FTP"); got != "FTP-3853" {
		t.Errorf("underscore-separated = %q, want FTP-3853", got)
	}
	if got := resolveTicketID("/nonexistent", "test/e2e-screenshot-toolkit", "FTP"); got != "" {
		t.Errorf("no ticket in branch = %q, want empty", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate kept = %q", got)
	}
	if got := truncate("0123456789abc", 10); got != "012345678…" {
		t.Errorf("truncate cut = %q", got)
	}
	// multibyte safety: result is at most n runes (n-1 + ellipsis)
	if got := truncate("кириллица текст здесь", 8); got != "кирилли…" {
		t.Errorf("truncate multibyte = %q", got)
	}
	// n<=1 must not panic on r[:n-1]
	if got := truncate("abc", 0); got != "…" {
		t.Errorf("truncate n=0 = %q, want …", got)
	}
}

func TestRenderSkipsOptionalSegments(t *testing.T) {
	in := &Input{}
	in.Model.DisplayName = "Opus 4.8 (1M context)"
	// Point at a non-repo dir so the result doesn't depend on where `go test`
	// runs (the package dir is itself a git repo).
	in.Workspace.CurrentDir = t.TempDir()
	out := plain(render(in, 0, 0))
	// Optional segments (git/PR/asana/MR/rate-limits) are skipped when absent,
	// but ctx + changes always render.
	want := "O 4.8 1M | ctx 0.0k (0%) | +0/-0 · 0m"
	if out != want {
		t.Errorf("render = %q, want %q", out, want)
	}
	for _, gone := range []string{"🌿", "🌳", "MR!", "PR#", "FTP-", "h "} {
		if strings.Contains(out, gone) {
			t.Errorf("bare input should not contain %q: %q", gone, out)
		}
	}
}

func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{grn + "abc" + rst, 3},        // ANSI escapes don't count
		{"🌳", 2},                      // emoji is two columns
		{"🌳 feat", 7},                 // emoji(2) + space + "feat"(4)
		{"ветка", 5},                  // Cyrillic is one column each
		{"FTP-3853 Backlog", 16},      // plain ASCII
	}
	for _, c := range cases {
		if got := visibleWidth(c.in); got != c.want {
			t.Errorf("visibleWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestAssembleWraps(t *testing.T) {
	model := "O 4.8 1M 🚀xh"
	git := "🌳 feat/FTP-3678-zoom-venue-image"
	asana := "FTP-3678 To Do"
	ctx := "ctx 0.0k (0%)"
	changes := "+0/-0 · 0m"

	// Wide terminal (or unknown width): everything stays on one line.
	for _, cols := range []int{0, 200} {
		one := assemble(model, git, asana, "", "", ctx, changes, "", cols)
		if strings.Contains(one, "\n") {
			t.Errorf("cols=%d should not wrap: %q", cols, plain(one))
		}
	}

	// Narrow terminal (the one-line form is ~95 cols, so 66 forces a wrap):
	// task + branch + session drop to a second row, task before the branch.
	const cols = 66
	two := assemble(model, git, asana, "", "", ctx, changes, "", cols)
	lines := strings.Split(plain(two), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 rows in narrow terminal, got %d: %q", len(lines), plain(two))
	}
	if !strings.Contains(lines[1], "FTP-3678 To Do") ||
		!strings.Contains(lines[1], "feat/FTP-3678") ||
		!strings.Contains(lines[1], "+0/-0") {
		t.Errorf("row 2 should carry task + branch + session: %q", lines[1])
	}
	if task, branch := strings.Index(lines[1], "FTP-3678 To Do"), strings.Index(lines[1], "feat/FTP-3678"); task >= branch {
		t.Errorf("row 2 should put the task code before the branch name: %q", lines[1])
	}
	if strings.Contains(lines[0], "FTP-3678 To Do") ||
		strings.Contains(lines[0], "feat/FTP-3678") ||
		strings.Contains(lines[0], "+0/-0") {
		t.Errorf("row 1 should not carry task/branch/session: %q", lines[0])
	}
	if !strings.Contains(lines[0], "O 4.8 1M") || !strings.Contains(lines[0], "ctx") {
		t.Errorf("row 1 should keep model + context: %q", lines[0])
	}
	// Splitting keeps each row within the width here (row 2 is ~63 cols).
	for i, ln := range strings.Split(two, "\n") {
		if w := visibleWidth(ln); w > cols {
			t.Errorf("row %d width %d exceeds cols=%d: %q", i, w, cols, plain(ln))
		}
	}
}

// --- helpers ---

type usage struct {
	Input, CacheCreate, CacheRead int
}

func mkCtx(size int, pct float64, u *usage) Input {
	in := Input{}
	in.ContextWindow = &ContextWindow{ContextWindowSize: size, UsedPercentage: pct}
	if u != nil {
		in.ContextWindow.CurrentUsage = &ContextUsage{
			InputTokens:              u.Input,
			CacheCreationInputTokens: u.CacheCreate,
			CacheReadInputTokens:     u.CacheRead,
		}
	}
	return in
}

func ptr(in Input) *Input { return &in }
