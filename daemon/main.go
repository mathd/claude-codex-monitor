// Claude + Codex usage monitor daemon.
//
// Reads usage from each vendor's read-only usage channel (Claude: oauth/usage
// endpoint; Codex: wham/usage), self-refreshing OAuth tokens, and publishes to an
// MQTT broker with Home Assistant auto-discovery. Works at home (HA reads the
// broker) and at the office (any device subscribes to the same broker).
//
// State topics (retained JSON):
//   claude_monitor/state        Claude  {session_pct, session_reset_min, week_pct, week_reset_min, ...}
//   claude_monitor/codex_state  Codex   (same shape)
//
// Claude fetching is in claude.go, Codex in codex.go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/joho/godotenv"
)

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

// ---- usage payload ----

type usage struct {
	SessionPct      int  `json:"session_pct"`
	SessionResetMin int  `json:"session_reset_min"`
	WeekPct         int  `json:"week_pct"`
	WeekResetMin    int  `json:"week_reset_min"`
	SonnetPct       int  `json:"sonnet_pct,omitempty"`
	SonnetResetMin  int  `json:"sonnet_reset_min,omitempty"`
	Ok              bool `json:"ok"`
	Stale           bool `json:"stale"`
}

// Claude usage fetching lives in claude.go; Codex in codex.go.

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
		// --- Claude (free read-only usage endpoint, self-refreshing token) ---
		u := fetchUsage(ctx)
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
