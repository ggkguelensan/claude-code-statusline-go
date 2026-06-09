package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// AsanaTask is the cached Asana state shown in the status line.
type AsanaTask struct {
	GID          string `json:"gid"`
	FTP          string `json:"ftp"`     // e.g. "FTP-3853" (the ticket custom field)
	Name         string `json:"name"`
	Section      string `json:"section"` // board column, e.g. "Backlog"
	Completed    bool   `json:"completed"`
	PermalinkURL string `json:"permalink_url"`
}

const asanaBase = "https://app.asana.com/api/1.0"

// asanaClient carries the PAT, so it gets an explicit timeout (independent of the
// caller's context) and refuses to follow redirects — the Bearer token must never
// be replayed to a host other than app.asana.com.
var asanaClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// asanaConfig is resolved from env (with Ticketon-friendly defaults) so the
// standalone binary can reach Asana without the claude.ai MCP.
type asanaConfig struct {
	token        string
	workspaceGID string
	ftpFieldGID  string
	ticketPrefix string
}

func loadAsanaConfig() asanaConfig {
	return asanaConfig{
		token:        firstEnv("ASANA_ACCESS_TOKEN", "ASANA_TOKEN", "ASANA_PAT"),
		workspaceGID: envOr("ASANA_WORKSPACE_GID", "1208507351529750"),
		ftpFieldGID:  envOr("ASANA_FTP_FIELD_GID", "1211799464714835"),
		ticketPrefix: envOr("ASANA_TICKET_PREFIX", "FTP"),
	}
}

// resolveTicketID returns the ticket id (e.g. FTP-3853) for the branch checked
// out in dir: an explicit `git config statusline.ftp`, else the first match of
// the ticket prefix in the branch name.
func resolveTicketID(dir, branch, prefix string) string {
	if v := gitConfig(dir, "statusline.ftp"); v != "" {
		return v
	}
	// Anchor on non-alphanumerics rather than \b: Go's \b treats "_" as a word
	// char, so a \b regex would miss "feat_FTP-3853". \d+ stops at the trailing
	// separator on its own, so no trailing anchor is needed.
	re := regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(` + regexp.QuoteMeta(prefix) + `-\d+)`)
	if m := re.FindStringSubmatch(branch); len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return ""
}

// fetchAsana resolves and fetches the task being worked on, or nil. Order:
//  1. explicit GID — git config statusline.asana-task / $ASANA_TASK_GID
//  2. ticket id (FTP-3853) searched via the FTP custom field
// Returns nil unless an Asana token is configured.
func fetchAsana(ctx context.Context, dir, branch string) *AsanaTask {
	cfg := loadAsanaConfig()
	if cfg.token == "" {
		return nil
	}
	if gid := firstNonEmpty(gitConfig(dir, "statusline.asana-task"), os.Getenv("ASANA_TASK_GID")); gid != "" {
		return asanaGetTask(ctx, cfg, gid)
	}
	if ticket := resolveTicketID(dir, branch, cfg.ticketPrefix); ticket != "" {
		return asanaSearchByFTP(ctx, cfg, ticket)
	}
	return nil
}

var asanaOptFields = "name,completed,permalink_url,memberships.section.name,custom_fields.name,custom_fields.display_value"

func asanaGetTask(ctx context.Context, cfg asanaConfig, gid string) *AsanaTask {
	u := fmt.Sprintf("%s/tasks/%s?opt_fields=%s", asanaBase, url.PathEscape(gid), url.QueryEscape(asanaOptFields))
	var resp struct {
		Data asanaTaskJSON `json:"data"`
	}
	if !asanaGet(ctx, cfg.token, u, &resp) || resp.Data.GID == "" {
		return nil
	}
	return resp.Data.toTask(cfg.ftpFieldPrefix())
}

func asanaSearchByFTP(ctx context.Context, cfg asanaConfig, ticket string) *AsanaTask {
	if cfg.workspaceGID == "" || cfg.ftpFieldGID == "" {
		return nil
	}
	// Asana search results are explicitly documented as unstable in ordering, so
	// when an FTP value maps to more than one task (re-created ticket, inherited
	// subtask field) pin the pick to the most recently modified one rather than
	// letting limit=1 return a nondeterministic — possibly stale — task.
	q := url.Values{}
	q.Set("custom_fields."+cfg.ftpFieldGID+".value", ticket)
	q.Set("opt_fields", asanaOptFields)
	q.Set("sort_by", "modified_at")
	q.Set("sort_ascending", "false")
	q.Set("limit", "1")
	u := fmt.Sprintf("%s/workspaces/%s/tasks/search?%s", asanaBase, url.PathEscape(cfg.workspaceGID), q.Encode())
	var resp struct {
		Data []asanaTaskJSON `json:"data"`
	}
	if !asanaGet(ctx, cfg.token, u, &resp) || len(resp.Data) == 0 {
		return nil
	}
	t := resp.Data[0].toTask(cfg.ftpFieldPrefix())
	if t.FTP == "" {
		t.FTP = ticket // search matched it even if the field wasn't returned
	}
	return t
}

// asanaGet performs an authenticated GET and decodes JSON into v; reports success.
func asanaGet(ctx context.Context, token, rawURL string, v any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := asanaClient.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	body := io.LimitReader(res.Body, 1<<20) // bound an oversized/hostile response
	if res.StatusCode != http.StatusOK {
		io.Copy(io.Discard, body)
		return false
	}
	return json.NewDecoder(body).Decode(v) == nil
}

type asanaTaskJSON struct {
	GID          string `json:"gid"`
	Name         string `json:"name"`
	Completed    bool   `json:"completed"`
	PermalinkURL string `json:"permalink_url"`
	Memberships  []struct {
		Section struct {
			Name string `json:"name"`
		} `json:"section"`
	} `json:"memberships"`
	CustomFields []struct {
		Name         string `json:"name"`
		DisplayValue string `json:"display_value"`
	} `json:"custom_fields"`
}

func (j asanaTaskJSON) toTask(ftpFieldName string) *AsanaTask {
	t := &AsanaTask{
		GID:          j.GID,
		Name:         j.Name,
		Completed:    j.Completed,
		PermalinkURL: j.PermalinkURL,
	}
	if len(j.Memberships) > 0 {
		t.Section = j.Memberships[0].Section.Name
	}
	for _, cf := range j.CustomFields {
		if strings.EqualFold(cf.Name, ftpFieldName) && cf.DisplayValue != "" {
			t.FTP = cf.DisplayValue
			break
		}
	}
	return t
}

// ftpFieldPrefix is the human name of the ticket custom field (matches by name).
func (c asanaConfig) ftpFieldPrefix() string { return c.ticketPrefix }

// segAsana renders the Asana task segment from cached data (empty when absent).
//
//	FTP-3853 Backlog          open task, board column
//	✓ FTP-3853 Done           completed
//	E2E screenshot toolkit…    no ticket id → truncated name
func segAsana(t *AsanaTask) string {
	if t == nil || t.GID == "" {
		return ""
	}
	var b strings.Builder
	if t.Completed {
		b.WriteString(grn + "✓" + rst + " ")
	}
	switch {
	case t.FTP != "":
		b.WriteString(mag + t.FTP + rst)
	case t.Name != "":
		b.WriteString(mag + truncate(t.Name, 24) + rst)
	default:
		b.WriteString(mag + "asana" + rst)
	}
	if t.Section != "" {
		b.WriteString(" " + dim + t.Section + rst)
	}
	return b.String()
}

// truncate shortens s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	if n < 1 {
		n = 1 // guard r[:n-1] against underflow for any future caller
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
