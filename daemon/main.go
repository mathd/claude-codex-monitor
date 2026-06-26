// Claude usage monitor daemon — MQTT edition.
//
// Reads Claude Code usage from Anthropic rate-limit response headers (the same
// approach as Claudial / Clawdmeter) and publishes them to an MQTT broker with
// Home Assistant MQTT auto-discovery. Works at home (HA reads the broker) and at
// the office (any device can subscribe to the same broker).
//
// Published state topic (default):  claude_monitor/state   (retained JSON)
//   {"session_pct":45,"session_reset_min":120,"week_pct":28,"week_reset_min":7200,"ok":true,"stale":false}
//
// HA discovery topics under homeassistant/sensor/claude_monitor_*/config make
// the sensors appear automatically (no YAML editing in HA needed).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/joho/godotenv"
)

const (
	apiURL = "https://api.anthropic.com/v1/messages"
)

var apiBody = []byte(`{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)

// ---- config ----

type config struct {
	pollInterval time.Duration
	brokerURL    string // e.g. tcp://192.168.1.50:1883
	mqttUser     string
	mqttPass     string
	clientID     string
	baseTopic    string // claude_monitor
	discoveryPre string // homeassistant
	deviceName   string // friendly name shown in HA
	deviceID     string // unique id slug
}

func loadConfig() config {
	if exe, err := os.Executable(); err == nil {
		_ = godotenv.Load(filepath.Join(filepath.Dir(exe), ".env"))
	}
	_ = godotenv.Load()

	cfg := config{
		pollInterval: 60 * time.Second,
		brokerURL:    "tcp://homeassistant.local:1883",
		clientID:     "claude-monitor-daemon",
		baseTopic:    "claude_monitor",
		discoveryPre: "homeassistant",
		deviceName:   "Claude Monitor",
		deviceID:     "claude_monitor",
	}
	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.pollInterval = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("MQTT_BROKER"); v != "" {
		cfg.brokerURL = v
	}
	if v := os.Getenv("MQTT_USER"); v != "" {
		cfg.mqttUser = v
	}
	if v := os.Getenv("MQTT_PASS"); v != "" {
		cfg.mqttPass = v
	}
	if v := os.Getenv("MQTT_CLIENT_ID"); v != "" {
		cfg.clientID = v
	}
	if v := os.Getenv("MQTT_BASE_TOPIC"); v != "" {
		cfg.baseTopic = v
	}
	if v := os.Getenv("HA_DISCOVERY_PREFIX"); v != "" {
		cfg.discoveryPre = v
	}
	return cfg
}

// ---- credentials (same logic as Claudial) ----

func loadToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	candidates := []string{
		filepath.Join(home, ".claude", ".credentials.json"),
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		candidates = append(candidates, filepath.Join(v, "Claude", ".credentials.json"))
	}
	if v := os.Getenv("APPDATA"); v != "" {
		candidates = append(candidates, filepath.Join(v, "Claude", ".credentials.json"))
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if tok := extractToken(raw); tok != "" {
			return tok, nil
		}
	}
	return "", fmt.Errorf("accessToken not found in credentials (run 'claude login')")
}

func extractToken(raw []byte) string {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err == nil {
		// Preferred: claudeAiOauth.accessToken (the Claude Code subscription token).
		// NOT mcpOAuth.*.accessToken, which is an unrelated MCP server token.
		if oauth, ok := data["claudeAiOauth"].(map[string]any); ok {
			if tok, ok := oauth["accessToken"].(string); ok && tok != "" {
				return tok
			}
		}
		// Fallback: a top-level accessToken (older credential layouts).
		if tok, ok := data["accessToken"].(string); ok && tok != "" {
			return tok
		}
	}
	return ""
}

// ---- usage payload ----

type usage struct {
	SessionPct      int  `json:"session_pct"`
	SessionResetMin int  `json:"session_reset_min"`
	WeekPct         int  `json:"week_pct"`
	WeekResetMin    int  `json:"week_reset_min"`
	Ok              bool `json:"ok"`
	Stale           bool `json:"stale"`
}

// fetchUsage makes the tiny API call and parses the rate-limit headers.
// Returns nil on a recoverable error (caller should fall back to cached/stale).
func fetchUsage(ctx context.Context, token string) *usage {
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(apiBody))
	if err != nil {
		log.Printf("request build error: %v", err)
		return nil
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-code/2.1.5")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("API error: %v", err)
		return nil
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode == 401 {
		log.Printf("API 401: token expired — run 'claude login' to refresh")
		return nil
	}
	if resp.StatusCode == 429 {
		log.Printf("Rate limited (429) — will retry next poll")
		return nil
	}
	if resp.StatusCode >= 400 {
		log.Printf("API HTTP %d", resp.StatusCode)
		return nil
	}

	now := float64(time.Now().Unix())
	hdr := func(name string) string { return resp.Header.Get(name) }

	pct := func(util string) (int, bool) {
		f, err := strconv.ParseFloat(strings.TrimSpace(util), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return int(math.Round(math.Max(0, math.Min(1, f)) * 100)), true
	}
	resetMin := func(ts string) int {
		f, err := strconv.ParseFloat(strings.TrimSpace(ts), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		mins := (f - now) / 60.0
		if mins < 0 {
			return 0
		}
		return int(math.Round(mins))
	}

	sUtil := hdr("anthropic-ratelimit-unified-5h-utilization")
	wUtil := hdr("anthropic-ratelimit-unified-7d-utilization")
	if sUtil == "" || wUtil == "" {
		log.Printf("Rate-limit headers missing in 2xx response")
		return nil
	}
	sPct, sOk := pct(sUtil)
	wPct, wOk := pct(wUtil)
	if !sOk || !wOk {
		log.Printf("Rate-limit headers unparseable (s=%q w=%q)", sUtil, wUtil)
		return nil
	}
	return &usage{
		SessionPct:      sPct,
		SessionResetMin: resetMin(hdr("anthropic-ratelimit-unified-5h-reset")),
		WeekPct:         wPct,
		WeekResetMin:    resetMin(hdr("anthropic-ratelimit-unified-7d-reset")),
		Ok:              true,
	}
}

// ---- MQTT + HA discovery ----

func stateTopic(cfg config) string      { return cfg.baseTopic + "/state" }
func availTopic(cfg config) string      { return cfg.baseTopic + "/availability" }
func codexStateTopic(cfg config) string { return cfg.baseTopic + "/codex_state" }

// haDevice is the shared device block so all sensors group under one HA device.
func haDevice(cfg config) map[string]any {
	return map[string]any{
		"identifiers":  []string{cfg.deviceID},
		"name":         cfg.deviceName,
		"manufacturer": "DIY",
		"model":        "Claude Usage Monitor",
	}
}

type discoverySensor struct {
	key       string // suffix for unique_id / object_id
	name      string
	valueTmpl string // jinja against the state JSON
	unit      string
	icon      string
	state     string // which state topic: "claude" or "codex"
}

func discoverySensors() []discoverySensor {
	return []discoverySensor{
		{"session_pct", "Claude Session Usage", "{{ value_json.session_pct }}", "%", "mdi:clock-fast", "claude"},
		{"session_reset_min", "Claude Session Reset", "{{ value_json.session_reset_min }}", "min", "mdi:timer-sand", "claude"},
		{"week_pct", "Claude Weekly Usage", "{{ value_json.week_pct }}", "%", "mdi:calendar-week", "claude"},
		{"week_reset_min", "Claude Weekly Reset", "{{ value_json.week_reset_min }}", "min", "mdi:timer-sand", "claude"},
		{"codex_session_pct", "Codex Session Usage", "{{ value_json.session_pct }}", "%", "mdi:clock-fast", "codex"},
		{"codex_session_reset_min", "Codex Session Reset", "{{ value_json.session_reset_min }}", "min", "mdi:timer-sand", "codex"},
		{"codex_week_pct", "Codex Weekly Usage", "{{ value_json.week_pct }}", "%", "mdi:calendar-week", "codex"},
		{"codex_week_reset_min", "Codex Weekly Reset", "{{ value_json.week_reset_min }}", "min", "mdi:timer-sand", "codex"},
	}
}

func publishDiscovery(client mqtt.Client, cfg config) {
	dev := haDevice(cfg)
	for _, s := range discoverySensors() {
		st := stateTopic(cfg)
		if s.state == "codex" {
			st = codexStateTopic(cfg)
		}
		topic := fmt.Sprintf("%s/sensor/%s_%s/config", cfg.discoveryPre, cfg.deviceID, s.key)
		payload := map[string]any{
			"name":                s.name,
			"unique_id":           cfg.deviceID + "_" + s.key,
			"state_topic":         st,
			"value_template":      s.valueTmpl,
			"unit_of_measurement": s.unit,
			"icon":                s.icon,
			"availability_topic":  availTopic(cfg),
			"device":              dev,
		}
		b, _ := json.Marshal(payload)
		// retained so HA rediscovers after a restart
		if t := client.Publish(topic, 1, true, b); t.Wait() && t.Error() != nil {
			log.Printf("discovery publish error (%s): %v", s.key, t.Error())
		}
	}
	log.Printf("Published HA discovery for %d sensors", len(discoverySensors()))
}

func publishCodexState(client mqtt.Client, cfg config, u *usage) {
	b, _ := json.Marshal(u)
	if t := client.Publish(codexStateTopic(cfg), 1, true, b); t.Wait() && t.Error() != nil {
		log.Printf("codex state publish error: %v", t.Error())
		return
	}
	log.Printf("Published codex: %s", b)
}

func publishState(client mqtt.Client, cfg config, u *usage) {
	b, _ := json.Marshal(u)
	if t := client.Publish(stateTopic(cfg), 1, true, b); t.Wait() && t.Error() != nil {
		log.Printf("state publish error: %v", t.Error())
		return
	}
	log.Printf("Published: %s", b)
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	cfg := loadConfig()
	log.Printf("Config: broker=%s poll=%s base=%s", cfg.brokerURL, cfg.pollInterval, cfg.baseTopic)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigCh; log.Println("Shutting down..."); cancel() }()

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.brokerURL).
		SetClientID(cfg.clientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10 * time.Second).
		SetKeepAlive(30 * time.Second).
		// Last Will: if the daemon dies, HA marks the sensors unavailable.
		SetWill(availTopic(cfg), "offline", 1, true)
	if cfg.mqttUser != "" {
		opts.SetUsername(cfg.mqttUser).SetPassword(cfg.mqttPass)
	}
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Printf("Connected to MQTT broker")
		publishDiscovery(c, cfg)
		c.Publish(availTopic(cfg), 1, true, "online")
	})

	client := mqtt.NewClient(opts)
	if t := client.Connect(); t.Wait() && t.Error() != nil {
		log.Fatalf("MQTT connect failed: %v", t.Error())
	}

	var cached *usage
	ticker := time.NewTicker(cfg.pollInterval)
	defer ticker.Stop()

	var codexCached *usage
	poll := func() {
		// --- Claude ---
		token, err := loadToken()
		if err != nil {
			log.Printf("Token error: %v", err)
			if cached != nil {
				cached.Stale = true
				publishState(client, cfg, cached)
			} else {
				publishState(client, cfg, &usage{Ok: false})
			}
		} else {
			u := fetchUsage(ctx, token)
			if u != nil {
				u.Stale = false
				cached = u
				publishState(client, cfg, u)
			} else if cached != nil {
				cached.Stale = true
				publishState(client, cfg, cached)
			} else {
				publishState(client, cfg, &usage{Ok: false})
			}
		}

		// --- Codex (best-effort; absent if no ~/.codex/auth.json) ---
		cu := fetchCodexUsage(ctx)
		if cu != nil {
			cu.Stale = false
			codexCached = cu
			publishCodexState(client, cfg, cu)
		} else if codexCached != nil {
			codexCached.Stale = true
			publishCodexState(client, cfg, codexCached)
		} else {
			publishCodexState(client, cfg, &usage{Ok: false})
		}
	}

	poll() // immediate first reading
	for {
		select {
		case <-ctx.Done():
			client.Publish(availTopic(cfg), 1, true, "offline").Wait()
			client.Disconnect(250)
			return
		case <-ticker.C:
			poll()
		}
	}
}
