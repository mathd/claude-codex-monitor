// Codex usage provider.
//
// Mirrors the OpenUsage/CrossUsage approach: read ~/.codex/auth.json, refresh the
// OAuth access token via auth.openai.com, then GET chatgpt.com/backend-api/wham/usage
// and read the weekly rate-limit window. OpenAI retired the 5h window, so only a
// weekly number is published — see the note on codexUsageResp.
//
// Auto-refreshes the token (so it works even when you haven't run Codex recently)
// and writes the refreshed tokens back to auth.json so the Codex CLI stays in sync.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	codexClientID   = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexRefreshURL = "https://auth.openai.com/oauth/token"
	codexUsageURL   = "https://chatgpt.com/backend-api/wham/usage"
)

// codexAuth mirrors ~/.codex/auth.json (only the fields we need).
type codexAuth struct {
	Tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	AccountID   string `json:"account_id"`
	LastRefresh string `json:"last_refresh"`
}

func codexAuthPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// Prefer ~/.codex; fall back to ~/.config/codex (matches the Codex CLI).
	for _, p := range []string{
		filepath.Join(home, ".codex", "auth.json"),
		filepath.Join(home, ".config", "codex", "auth.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no codex auth.json found")
}

func loadCodexAuth() (*codexAuth, string, error) {
	p, err := codexAuthPath()
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	var a codexAuth
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, "", fmt.Errorf("parse codex auth: %w", err)
	}
	return &a, p, nil
}

// refreshCodexToken exchanges the refresh token for a fresh access token and
// writes the new tokens back to auth.json (best-effort).
func refreshCodexToken(ctx context.Context, a *codexAuth, path string) (string, error) {
	if a.Tokens.RefreshToken == "" {
		return "", fmt.Errorf("no codex refresh_token")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", codexClientID)
	form.Set("refresh_token", a.Tokens.RefreshToken)

	req, err := http.NewRequestWithContext(ctx, "POST", codexRefreshURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("codex refresh HTTP %d", resp.StatusCode)
	}
	var rr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &rr); err != nil || rr.AccessToken == "" {
		return "", fmt.Errorf("codex refresh: no access_token")
	}

	// Persist the rotated tokens back to auth.json so the Codex CLI stays in sync.
	a.Tokens.AccessToken = rr.AccessToken
	if rr.RefreshToken != "" {
		a.Tokens.RefreshToken = rr.RefreshToken
	}
	if rr.IDToken != "" {
		a.Tokens.IDToken = rr.IDToken
	}
	a.LastRefresh = time.Now().UTC().Format(time.RFC3339)
	if out, err := json.MarshalIndent(a, "", "  "); err == nil {
		_ = os.WriteFile(path, out, 0600)
	}
	return rr.AccessToken, nil
}

// codexWindow is one rate-limit window from the usage response.
// LimitWindowSeconds identifies WHICH window this is — see below.
type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetAfterSeconds  int     `json:"reset_after_seconds"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
}

// OpenAI retired the 5h window. The weekly window MOVED INTO primary_window
// (limit_window_seconds: 604800) and secondary_window is now null — the field
// names stayed put while their meaning shifted, so mapping primary->session
// silently mislabels weekly data as a session. Classify by the window's own
// duration rather than trusting its position.
type codexUsageResp struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		PrimaryWindow   *codexWindow `json:"primary_window"`
		SecondaryWindow *codexWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

// weeklyWindow returns whichever window covers ~a week (>= 24h), tolerating the
// field shuffle. Returns nil if neither slot holds one.
func (u *codexUsageResp) weeklyWindow() *codexWindow {
	const daySeconds = 86400
	for _, w := range []*codexWindow{u.RateLimit.PrimaryWindow, u.RateLimit.SecondaryWindow} {
		if w != nil && w.LimitWindowSeconds >= daySeconds {
			return w
		}
	}
	return nil
}

// fetchCodexUsage refreshes the token and fetches usage. Returns a *usage with
// only the weekly fields set (session stays 0 — OpenAI retired the 5h window).
func fetchCodexUsage(ctx context.Context) *usage {
	a, path, err := loadCodexAuth()
	if err != nil {
		return nil // no codex configured — caller treats as unavailable
	}
	acct := a.Tokens.AccountID
	if acct == "" {
		acct = a.AccountID
	}

	token, err := refreshCodexToken(ctx, a, path)
	if err != nil {
		// Fall back to the existing (possibly still-valid) access token.
		token = a.Tokens.AccessToken
		if token == "" {
			return nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", codexUsageURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "claude-monitor")
	if acct != "" {
		req.Header.Set("ChatGPT-Account-Id", acct)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { io.Copy(io.Discard, resp.Body); resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var u codexUsageResp
	if err := json.Unmarshal(body, &u); err != nil {
		return nil
	}

	pct := func(p float64) int {
		return int(math.Round(math.Max(0, math.Min(100, p))))
	}
	mins := func(secs int) int {
		if secs < 0 {
			return 0
		}
		return int(math.Round(float64(secs) / 60.0))
	}
	w := u.weeklyWindow()
	if w == nil {
		return nil // no usable window — caller treats as unavailable
	}
	// Session stays 0: OpenAI no longer publishes a 5h window.
	// Unlike Claude, the window length is in the response — no constant needed.
	weekReset := mins(w.ResetAfterSeconds)
	return &usage{
		WeekPct:        pct(w.UsedPercent),
		WeekResetMin:   weekReset,
		WeekElapsedPct: elapsedPct(weekReset, mins(w.LimitWindowSeconds)),
		Ok:             true,
	}
}
