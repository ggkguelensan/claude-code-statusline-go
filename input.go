package main

// Input mirrors the JSON Claude Code writes to the status line command on
// stdin. See https://code.claude.com/docs/en/statusline. Only the fields the
// status line consumes are declared; everything else is ignored.
//
// Pointers are used where "absent" and "zero" must be distinguished (e.g. a
// rate-limit window with no reset time vs. a reset time of 0).
type Input struct {
	Model         ModelInfo      `json:"model"`
	Effort        *EffortInfo    `json:"effort"`
	Workspace     Workspace      `json:"workspace"`
	Cwd           string         `json:"cwd"`
	Worktree      *WorktreeInfo  `json:"worktree"`
	PR            *PRInfo        `json:"pr"`
	ContextWindow *ContextWindow `json:"context_window"`
	Exceeds200k   bool           `json:"exceeds_200k_tokens"`
	Cost          *CostInfo      `json:"cost"`
	RateLimits    *RateLimits    `json:"rate_limits"`
}

type ModelInfo struct {
	DisplayName string `json:"display_name"`
}

type EffortInfo struct {
	Level string `json:"level"`
}

type Workspace struct {
	CurrentDir string   `json:"current_dir"`
	AddedDirs  []string `json:"added_dirs"`
}

// WorktreeInfo is present only in `--worktree` sessions.
type WorktreeInfo struct {
	OriginalBranch string `json:"original_branch"`
}

// PRInfo is the GitHub PR associated with the branch (absent once merged/closed).
type PRInfo struct {
	Number      int    `json:"number"`
	ReviewState string `json:"review_state"`
}

type ContextWindow struct {
	ContextWindowSize int           `json:"context_window_size"`
	UsedPercentage    float64       `json:"used_percentage"`
	CurrentUsage      *ContextUsage `json:"current_usage"`
}

type ContextUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type CostInfo struct {
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalLinesAdded   int     `json:"total_lines_added"`
	TotalLinesRemoved int     `json:"total_lines_removed"`
	TotalDurationMS   int64   `json:"total_duration_ms"`
}

type RateLimits struct {
	FiveHour *RLWindow `json:"five_hour"`
	SevenDay *RLWindow `json:"seven_day"`
}

// RLWindow is one subscription rate-limit window.
type RLWindow struct {
	ResetsAt       *float64 `json:"resets_at"`
	UsedPercentage float64  `json:"used_percentage"`
}
