# Claude Monitor Daemon (MQTT)

Reads Claude Code usage from Anthropic rate-limit response headers and publishes
to MQTT with Home Assistant auto-discovery. Runs on your **Windows PC** (the one
where you use Claude Code — it needs `~/.claude/.credentials.json`).

## What it does

Every `POLL_INTERVAL` seconds it makes a tiny Haiku API call (~9 tokens, ~$0.02/day)
and reads these response headers:

- `anthropic-ratelimit-unified-5h-utilization` / `-reset`  → session %, reset
- `anthropic-ratelimit-unified-7d-utilization` / `-reset`  → weekly %, reset

It publishes a retained JSON state to `claude_monitor/state` and HA discovery
configs so four sensors appear automatically in Home Assistant:

- `sensor.claude_session_usage` (%)
- `sensor.claude_session_reset` (min)
- `sensor.claude_weekly_usage` (%)
- `sensor.claude_weekly_reset` (min)

A Last-Will message marks them **unavailable** if the daemon stops.

## Setup

1. Make sure you've run `claude login` so `~/.claude/.credentials.json` exists.
2. Copy `.env.example` to `.env` and set `MQTT_BROKER` + `MQTT_USER`/`MQTT_PASS`.
   - Create the MQTT user in HA's Mosquitto add-on (or reuse your HA login).
3. Build and run (see below).

## Build / run on Windows

With Go installed on Windows:

```powershell
cd daemon-mqtt
go build -o claude-monitor-daemon.exe .
.\claude-monitor-daemon.exe
```

Or cross-compile from this Linux/WSL checkout (no Go needed on Windows):

```bash
cd daemon-mqtt
GOOS=windows GOARCH=amd64 go build -o claude-monitor-daemon.exe .
# copy the .exe + your .env to the Windows PC and run it
```

Put `.env` in the same folder as the `.exe`.

## Run as a service (WSL — current setup)

Installed as a systemd service (`/etc/systemd/system/claude-monitor.service`):

```bash
sudo systemctl status claude-monitor    # check
sudo systemctl restart claude-monitor   # after rebuilding the binary
journalctl -u claude-monitor -f         # follow logs
```

Auto-starts on WSL boot, auto-restarts on crash. NOTE: WSL itself only runs while
Windows has it started — to bring WSL (and thus this service) up automatically at
Windows logon, add a Task Scheduler entry that runs `wsl.exe -d <distro> true` at logon.

## Run at startup (Windows)

Simplest: drop a shortcut to the `.exe` in
`shell:startup` (Win+R → `shell:startup`). Or use Task Scheduler for a hidden,
auto-restart service.

## Notes / caveats

- The OAuth token from `claude login` expires every few hours. When it does, the
  daemon logs a 401 and publishes the last value as **stale** until you next use
  Claude Code (which refreshes the token) — same behavior as Claudial.
- There is no Codex equivalent of these headers, so a future Codex tile needs a
  different data source.
- Office use: point `MQTT_BROKER` at a local broker
  (`docker run -p 1883:1883 eclipse-mosquitto`) — no Home Assistant required.
