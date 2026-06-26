# Usage Monitor Daemon

Reads your **Claude Code** and **Codex** usage and publishes it to MQTT with Home
Assistant auto-discovery. Runs on the machine where you use those tools (it needs
their local credentials).

## What it does

Every `POLL_INTERVAL` seconds:

- **Claude** — makes a tiny Haiku API call (~9 tokens, ~$0.02/day) with the OAuth
  token from `~/.claude/.credentials.json`, reads the rate-limit response headers:
  - `anthropic-ratelimit-unified-5h-utilization` / `-reset` → session %, reset
  - `anthropic-ratelimit-unified-7d-utilization` / `-reset` → weekly %, reset
- **Codex** — refreshes the OAuth token from `~/.codex/auth.json` against
  `auth.openai.com`, then GETs `chatgpt.com/backend-api/wham/usage` and reads the
  `primary_window` (5h) and `secondary_window` (weekly). Auto-refresh means the
  Codex tile shows live data even if you haven't run Codex recently.

It publishes retained JSON to `claude_monitor/state` (Claude) and
`claude_monitor/codex_state` (Codex), plus HA discovery so **8 sensors** appear
automatically:

```
sensor.claude_monitor_claude_session_usage / _session_reset / _weekly_usage / _weekly_reset
sensor.claude_monitor_codex_session_usage  / _session_reset / _weekly_usage / _weekly_reset
```

A Last-Will message marks them **unavailable** if the daemon stops. If a provider's
credentials are missing or expired, that provider publishes `ok:false`/stale; the
other still works.

## Setup

1. `claude login` (so `~/.claude/.credentials.json` exists). For Codex, sign into the
   Codex CLI once (so `~/.codex/auth.json` exists).
2. Copy `.env.example` → `.env` and set `MQTT_BROKER`, `MQTT_USER`, `MQTT_PASS`.
   - The MQTT user can be a Home Assistant user (the Mosquitto add-on accepts them).
3. Build and run.

## Build / run

```bash
cd daemon
go build -o claude-monitor-daemon .
./claude-monitor-daemon
```

Cross-compile a Windows binary (no Go needed on Windows):

```bash
GOOS=windows GOARCH=amd64 go build -o claude-monitor-daemon.exe .
```

Keep `.env` in the same directory as the binary.

## Run as a service (systemd, WSL/Linux)

`claude-monitor.service` is included. Install once:

```bash
sudo cp claude-monitor.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now claude-monitor
```

Then:

```bash
sudo systemctl restart claude-monitor   # after rebuilding the binary
journalctl -u claude-monitor -f         # follow logs
```

Auto-starts on boot, auto-restarts on crash. (In WSL, WSL itself only runs while
Windows keeps it up — add a Task Scheduler entry running `wsl.exe -d <distro> true`
at logon for hands-free start.)

## Config (`.env`)

| Key | Default | Notes |
|-----|---------|-------|
| `MQTT_BROKER` | `tcp://homeassistant.local:1883` | your broker |
| `MQTT_USER` / `MQTT_PASS` | – | MQTT credentials |
| `POLL_INTERVAL` | `60` | seconds; raise (e.g. 120) if you hit rate limits |
| `MQTT_BASE_TOPIC` | `claude_monitor` | topic prefix |
| `HA_DISCOVERY_PREFIX` | `homeassistant` | HA discovery prefix |

## Notes

- Office use without Home Assistant: point `MQTT_BROKER` at any local broker
  (`docker run -p 1883:1883 eclipse-mosquitto`).
- See [../LEARNINGS.md](../LEARNINGS.md) for the credential/endpoint details (nested
  Claude token, required Codex account header, etc.).
