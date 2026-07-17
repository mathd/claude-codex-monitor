// Claude usage provider.
//
// Reads usage from Anthropic's read-only OAuth usage endpoint (the same one the
// Claude clients use) — NOT a billed inference call. Free, no rate-limit exposure,
// and returns structured windows: five_hour (session), seven_day (weekly), plus a
// model-scoped weekly window inside the `limits` array (currently Fable).
//
// Token from ~/.claude/.credentials.json (claudeAiOauth). If the access token is
// rejected, refresh it via platform.claude.com and retry (writing the rotated
// tokens back so the Claude CLI stays in sync).
//
// Method mirrors OpenUsage (Sources/OpenUsage/Providers/Claude/*).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	claudeUsageURL   = "https://api.anthropic.com/api/oauth/usage"
	claudeRefreshURL = "https://platform.claude.com/v1/oauth/token"
	claudeClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeScopes     = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	claudeBetaHdr    = "oauth-2025-04-20"
	claudeUA         = "claude-code/2.1.69"

	// Window lengths for the pace marker. The API publishes resets_at but never
	// the window's length, so these are constants. Verified against live data:
	// the windows are a FIXED GRID, not rolling — five_hour lands on a clean
	// :30:00 boundary and seven_day on Saturday 11:00 UTC exactly. If a reset
	// ever stops landing on a round boundary, revisit these.
	claudeSessionWindowMin = 5 * 60      // five_hour
	claudeWeekWindowMin    = 7 * 24 * 60 // seven_day
)

func claudeCredPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", ".credentials.json"), nil
}

// claudeTokens returns (accessToken, refreshToken).
func claudeTokens() (string, string) {
	p, err := claudeCredPath()
	if err != nil {
		return "", ""
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", ""
	}
	var d map[string]any
	if json.Unmarshal(raw, &d) != nil {
		return "", ""
	}
	if o, ok := d["claudeAiOauth"].(map[string]any); ok {
		at, _ := o["accessToken"].(string)
		rt, _ := o["refreshToken"].(string)
		return at, rt
	}
	return "", ""
}

// refreshClaudeToken exchanges the refresh token, persists the rotated tokens back
// to ~/.claude/.credentials.json, and returns the new access token.
func refreshClaudeToken(ctx context.Context, refreshToken string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeClientID,
		"scope":         claudeScopes,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", claudeRefreshURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != 200 {
		return "", &httpErr{resp.StatusCode}
	}
	var m struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(rb, &m) != nil || m.AccessToken == "" {
		return "", &parseErr{"claude refresh: no access_token"}
	}
	persistClaudeTokens(m.AccessToken, m.RefreshToken)
	return m.AccessToken, nil
}

// persistClaudeTokens updates claudeAiOauth.accessToken/refreshToken in place,
// preserving every other field in the credentials file. Best-effort.
func persistClaudeTokens(access, refresh string) {
	p, err := claudeCredPath()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var d map[string]any
	if json.Unmarshal(raw, &d) != nil {
		return
	}
	o, ok := d["claudeAiOauth"].(map[string]any)
	if !ok {
		return
	}
	o["accessToken"] = access
	if refresh != "" {
		o["refreshToken"] = refresh
	}
	if out, err := json.MarshalIndent(d, "", "  "); err == nil {
		_ = os.WriteFile(p, out, 0600)
	}
}

// claudeUsageResp is the subset of /api/oauth/usage we use.
type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"` // RFC3339, may be null
}

// claudeLimit is one entry of the `limits` array — the newer shape that carries
// the model-scoped weekly window. The scoped model is DATA, not schema: the kind
// is the generic "weekly_scoped" and the model name lives in scope.model. The
// now-null seven_day_opus / seven_day_sonnet fields show this slot has already
// changed models once, so match on kind and never on an array index.
// Percent is a pointer so an omitted/null field is distinguishable from a real
// 0% — otherwise a schema change would silently publish "Fable 0%" as healthy.
type claudeLimit struct {
	Group   string   `json:"group"`
	Kind    string   `json:"kind"`
	Percent *float64 `json:"percent"`
	Scope   *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

type claudeUsageResp struct {
	FiveHour claudeWindow  `json:"five_hour"`
	SevenDay claudeWindow  `json:"seven_day"`
	Limits   []claudeLimit `json:"limits"`
}

// scopedWeekly returns the model-scoped weekly window's utilization, and whether
// a usable one was present. Absent (older account shape, or the slot retired),
// or present but with no percent -> false, so the caller can show "--" instead
// of a fake 0%.
//
// Deliberately matched on group+kind rather than scope.model.display_name: the
// scoped model is data, not schema (this slot held Opus and Sonnet before Fable
// — see the now-null seven_day_opus/seven_day_sonnet fields). Matching the name
// would break silently the next time Anthropic swaps the model. If the API ever
// returns more than one weekly_scoped entry, this takes the first; today there
// is exactly one.
func (u *claudeUsageResp) scopedWeekly() (float64, bool) {
	for _, l := range u.Limits {
		if l.Group == "weekly" && l.Kind == "weekly_scoped" && l.Percent != nil {
			return *l.Percent, true
		}
	}
	return 0, false
}

func claudeGetUsage(ctx context.Context, token string) (*claudeUsageResp, int) {
	req, err := http.NewRequestWithContext(ctx, "GET", claudeUsageURL, nil)
	if err != nil {
		return nil, 0
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", claudeBetaHdr)
	req.Header.Set("User-Agent", claudeUA)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0
	}
	defer func() { io.Copy(io.Discard, resp.Body); resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, resp.StatusCode
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var u claudeUsageResp
	if json.Unmarshal(body, &u) != nil {
		return nil, resp.StatusCode
	}
	return &u, 200
}

func resetMinFromRFC3339(ts string) int {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}
	mins := time.Until(t).Minutes()
	if mins < 0 {
		return 0
	}
	return int(math.Round(mins))
}

// fetchUsage reads Claude usage, refreshing the token once on 401/403. Returns a
// *usage (session = five_hour, week = seven_day, fable = the weekly_scoped limit)
// or nil on failure.
func fetchUsage(ctx context.Context) *usage {
	access, refresh := claudeTokens()
	if access == "" {
		log.Printf("Claude: no token (run 'claude login')")
		return nil
	}

	u, code := claudeGetUsage(ctx, access)
	if (code == 401 || code == 403) && refresh != "" {
		log.Printf("Claude: token rejected (%d), refreshing...", code)
		if newTok, err := refreshClaudeToken(ctx, refresh); err == nil {
			u, code = claudeGetUsage(ctx, newTok)
		} else {
			log.Printf("Claude refresh failed: %v", err)
		}
	}
	if u == nil {
		log.Printf("Claude usage fetch failed (HTTP %d)", code)
		return nil
	}

	pct := func(p float64) int { return int(math.Round(math.Max(0, math.Min(100, p)))) }

	// nil (not 0) when absent, so the ring reads "--" rather than a fake 0%.
	var fable *int
	if f, ok := u.scopedWeekly(); ok {
		v := pct(f)
		fable = &v
	} else {
		log.Printf("Claude: no usable weekly_scoped limit in response — Fable will show unavailable")
	}

	sessionReset := resetMinFromRFC3339(u.FiveHour.ResetsAt)
	weekReset := resetMinFromRFC3339(u.SevenDay.ResetsAt)

	return &usage{
		SessionPct:        pct(u.FiveHour.Utilization),
		SessionResetMin:   sessionReset,
		SessionElapsedPct: elapsedPct(sessionReset, claudeSessionWindowMin),
		WeekPct:           pct(u.SevenDay.Utilization),
		WeekResetMin:      weekReset,
		WeekElapsedPct:    elapsedPct(weekReset, claudeWeekWindowMin),
		FablePct:          fable,
		Ok:                true,
	}
}

// small error types
type httpErr struct{ code int }

func (e *httpErr) Error() string { return "HTTP " + itoa(e.code) }

type parseErr struct{ msg string }

func (e *parseErr) Error() string { return e.msg }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
