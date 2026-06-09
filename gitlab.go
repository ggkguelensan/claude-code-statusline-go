package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"time"
)

// MR is the cached GitLab merge-request state shown in the status line.
type MR struct {
	IID                        int    `json:"iid"`
	Title                      string `json:"title"`
	WebURL                     string `json:"web_url"`
	Draft                      bool   `json:"draft"`
	HasConflicts               bool   `json:"has_conflicts"`
	DetailedMergeStatus        string `json:"detailed_merge_status"`
	PipelineStatus             string `json:"pipeline_status"`
	BlockingDiscussionsResolved bool  `json:"blocking_discussions_resolved"`
}

// glabMR is the subset of GitLab's MR JSON the fetch decodes.
type glabMR struct {
	IID                         int    `json:"iid"`
	Title                       string `json:"title"`
	WebURL                      string `json:"web_url"`
	Draft                       bool   `json:"draft"`
	HasConflicts                bool   `json:"has_conflicts"`
	DetailedMergeStatus         string `json:"detailed_merge_status"`
	BlockingDiscussionsResolved bool   `json:"blocking_discussions_resolved"`
	HeadPipeline                *struct {
		Status string `json:"status"`
	} `json:"head_pipeline"`
}

// fetchMR finds the open MR whose source branch is the one checked out in dir,
// using glab (which resolves host + project from the repo's origin remote).
// Returns nil when glab is missing, unauthenticated, or no such MR exists.
//
// Two calls: the list endpoint maps branch→iid (it omits head_pipeline), then
// the detail endpoint fills in the pipeline status. The detail call is cheap and
// only runs in the background refresh, never on the foreground render.
func fetchMR(ctx context.Context, dir, branch string) *MR {
	if branch == "" || _glabPath() == "" {
		return nil
	}
	q := url.Values{}
	q.Set("source_branch", branch)
	q.Set("state", "opened")

	var list []glabMR
	if !glabAPI(ctx, dir, "projects/:id/merge_requests?"+q.Encode(), &list) || len(list) == 0 {
		return nil
	}
	g := list[0]

	// Re-fetch the single MR for fields the list view omits (head_pipeline).
	var detail glabMR
	if glabAPI(ctx, dir, fmt.Sprintf("projects/:id/merge_requests/%d", g.IID), &detail) && detail.IID != 0 {
		g = detail
	}

	mr := &MR{
		IID:                         g.IID,
		Title:                       g.Title,
		WebURL:                      g.WebURL,
		Draft:                       g.Draft,
		HasConflicts:                g.HasConflicts,
		DetailedMergeStatus:         g.DetailedMergeStatus,
		BlockingDiscussionsResolved: g.BlockingDiscussionsResolved,
	}
	if g.HeadPipeline != nil {
		mr.PipelineStatus = g.HeadPipeline.Status
	}
	return mr
}

// glabAPI runs `glab api <path>` in dir and decodes the JSON body into v.
func glabAPI(ctx context.Context, dir, path string, v any) bool {
	cmd := exec.CommandContext(ctx, "glab", "api", path)
	cmd.Dir = dir // glab reads host + project from the origin remote here
	out, err := cmd.Output()
	if err != nil {
		if os.Getenv("CCSL_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "glabAPI %q error: %v\n", path, err)
		}
		return false
	}
	if jerr := json.Unmarshal(out, v); jerr != nil {
		if os.Getenv("CCSL_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "glabAPI %q decode error: %v\n", path, jerr)
		}
		return false
	}
	return true
}

var glabPathCache *string

func _glabPath() string {
	if glabPathCache == nil {
		p, _ := exec.LookPath("glab")
		glabPathCache = &p
	}
	return *glabPathCache
}

// pipelineGlyph maps a GitLab pipeline status to a colored single-char glyph.
func pipelineGlyph(status string) string {
	switch status {
	case "success":
		return " " + grn + "✓" + rst
	case "failed", "canceled", "canceling":
		return " " + red + "✗" + rst
	case "running", "pending", "created", "waiting_for_resource", "preparing", "scheduled":
		return " " + ylw + "●" + rst
	default: // skipped, manual, "", unknown
		return ""
	}
}

// segMR renders the GitLab MR segment from cached data (empty when absent).
//
//	MR!1297 ✓        merged-ready, pipeline green
//	MR!1297 📝 ●     draft, pipeline running
//	MR!1297 ❗        merge conflicts
func segMR(mr *MR) string {
	if mr == nil || mr.IID == 0 {
		return ""
	}
	s := fmt.Sprintf("%sMR%s%s!%d%s", dim, rst, cyn, mr.IID, rst)
	if mr.Draft {
		s += " 📝"
	}
	if mr.HasConflicts {
		s += " " + red + "❗" + rst
	} else {
		s += pipelineGlyph(mr.PipelineStatus)
	}
	if !mr.BlockingDiscussionsResolved {
		s += " " + ylw + "💬" + rst
	}
	return s
}

// glabTimeout bounds a single glab invocation during a background refresh.
const glabTimeout = 8 * time.Second
