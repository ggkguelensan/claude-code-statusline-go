package main

import (
	"fmt"
	"regexp"
	"strings"
)

// prIcons maps a GitHub PR review_state to its trailing icon.
var prIcons = map[string]string{
	"approved":          " ✅",
	"changes_requested": " ❌",
	"draft":             " 📝",
	"pending":           " 👀",
}

// effortAbbr abbreviates the reasoning-effort level.
var effortAbbr = map[string]string{
	"low": "lo", "medium": "med", "high": "hi", "xhigh": "xh", "max": "max",
}

var ctxReplace = regexp.MustCompile(`\((\d+[KM]) context\)`)

// shortModel: "Opus 4.8 (1M context)" -> "O 4.8 1M", "Sonnet 4.6" -> "S 4.6".
func shortModel(name string) string {
	name = ctxReplace.ReplaceAllString(name, "$1")
	parts := strings.Fields(name)
	if len(parts) > 0 && isAlpha(parts[0]) {
		parts[0] = parts[0][:1]
	}
	return strings.Join(parts, " ")
}

func isAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

// rlColor: red ≥90% used, yellow ≥70%, else dim.
func rlColor(pct float64) string {
	switch {
	case pct >= 90:
		return red
	case pct >= 70:
		return ylw
	default:
		return dim
	}
}

// segModel builds the model (+ reasoning effort) segment.
func segModel(in *Input) string {
	name := in.Model.DisplayName
	if name == "" {
		name = "?"
	}
	s := bld + cyn + shortModel(name) + rst
	if in.Effort != nil && in.Effort.Level != "" {
		abbr, ok := effortAbbr[in.Effort.Level]
		if !ok {
			abbr = in.Effort.Level
		}
		s += " " + mag + "⚡" + abbr + rst
	}
	return s
}

// segGit builds the multi-repo git segment and returns the deduped repos so the
// caller can reuse the primary one for the MR/Asana lookups.
func segGit(in *Input) (string, []*RepoInfo) {
	cwd := in.Workspace.CurrentDir
	if cwd == "" {
		cwd = in.Cwd
	}
	if cwd == "" {
		cwd = "."
	}
	dirs := append([]string{cwd}, in.Workspace.AddedDirs...)

	var repos []*RepoInfo
	seen := map[string]bool{}
	for _, d := range dirs {
		info := repoInfo(d)
		if info != nil && !seen[info.Root] {
			seen[info.Root] = true
			repos = append(repos, info)
		}
	}
	if len(repos) == 0 {
		return "", nil
	}

	multi := len(repos) > 1
	var parts []string
	for i, r := range repos {
		icon := "🌿"
		if r.IsWorktree {
			icon = "🌳"
		}
		prefix := ""
		if multi {
			prefix = dim + r.Name + ":" + rst
		}
		entry := fmt.Sprintf("%s %s%s%s%s", icon, prefix, grn, r.Branch, rst)
		if r.Dirty {
			entry += ylw + "*" + rst
		}
		if i == 0 && r.IsWorktree && in.Worktree != nil && in.Worktree.OriginalBranch != "" {
			entry += " " + dim + "← " + in.Worktree.OriginalBranch + rst
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, dot), repos
}

// segPR builds the GitHub PR segment (empty when no open PR).
func segPR(in *Input) string {
	if in.PR == nil || in.PR.Number == 0 {
		return ""
	}
	return fmt.Sprintf("PR#%d%s", in.PR.Number, prIcons[in.PR.ReviewState])
}

// segContext builds the context-window segment. Absolute tokens lead because on
// 1M-context models the percentage hides degradation (context rot starts at
// ~80–100k tokens regardless of window size).
func segContext(in *Input) string {
	const ctxWarn, ctxBad = 80_000, 100_000
	// Always rendered (matches the Python original): a missing context_window —
	// e.g. before the first API response — still prints "ctx 0.0k (0%)".
	var cw ContextWindow
	if in.ContextWindow != nil {
		cw = *in.ContextWindow
	}

	var tokens int
	if cw.CurrentUsage != nil {
		u := cw.CurrentUsage
		tokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	}
	if tokens == 0 { // before the first API response current_usage is null
		tokens = int(float64(cw.ContextWindowSize) * cw.UsedPercentage / 100)
	}

	color := grn
	switch {
	case tokens >= ctxBad:
		color = red
	case tokens >= ctxWarn:
		color = ylw
	}

	var tok string
	if tokens >= 1_000_000 {
		tok = fmt.Sprintf("%.2fM", float64(tokens)/1e6)
	} else {
		tok = fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}

	over := ""
	if in.Exceeds200k {
		over = " ⚠"
	}
	pct := int(cw.UsedPercentage)
	return fmt.Sprintf("ctx %s%s%s %s(%d%%)%s%s", color, tok, rst, dim, pct, rst, over)
}

// segCost builds the cost · +/- lines · duration segment. Always rendered
// (matches the Python original): a missing cost field still prints
// "$0.00 · +0/-0 · 0m".
func segCost(in *Input) string {
	var c CostInfo
	if in.Cost != nil {
		c = *in.Cost
	}
	mins := c.TotalDurationMS / 60000
	var t string
	if mins >= 60 {
		t = fmt.Sprintf("%dh%dm", mins/60, mins%60)
	} else {
		t = fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("$%.2f%s%s+%d%s/%s-%d%s%s%s",
		c.TotalCostUSD, dot, grn, c.TotalLinesAdded, rst, red, c.TotalLinesRemoved, rst, dot, t)
}

// segRateLimits builds the subscription rate-limit countdown segment.
func segRateLimits(in *Input, now float64) string {
	if in.RateLimits == nil {
		return ""
	}
	type win struct {
		w    *RLWindow
		unit string
		div  float64
	}
	wins := []win{
		{in.RateLimits.FiveHour, "h", 3600},
		{in.RateLimits.SevenDay, "d", 86400},
	}
	var parts []string
	for _, x := range wins {
		if x.w == nil || x.w.ResetsAt == nil {
			continue
		}
		left := (*x.w.ResetsAt - now) / x.div
		if left < 0 {
			left = 0
		}
		used := x.w.UsedPercentage
		parts = append(parts, fmt.Sprintf("%s%.1f%s%s %s%d%%%s",
			rlColor(used), left, x.unit, rst, dim, int(used), rst))
	}
	return strings.Join(parts, dot)
}
