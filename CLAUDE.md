# CLAUDE.md

Guidance for AI agents (and humans) working in this repository.

## What this is

A physical Claude/Codex usage monitor: a Go daemon reads usage from vendor side
channels and publishes it over MQTT to Home Assistant; an ESP32-S3 round touch
display (ESPHome) shows it as swipeable arc gauges. See [README.md](README.md) for
the full architecture and [LEARNINGS.md](LEARNINGS.md) for the non-obvious facts.

## Repo map

- `firmware/claude-monitor.yaml` — the entire device firmware (ESPHome). Display
  driver, touch, LVGL tileview UI, and the `homeassistant` sensor bindings all live
  here.
- `daemon/` — Go MQTT daemon. `main.go` = config, Claude provider, MQTT/HA discovery,
  poll loop. `codex.go` = Codex provider (token refresh + usage). One Go module.
- `docs/reference/waveshare-demo/` — the **authoritative** vendor source for pins and
  the ST7701 init sequence. When in doubt about hardware, this is ground truth.
- `docs/hardware/` — the board PDF.

## Golden rules

1. **Read [LEARNINGS.md](LEARNINGS.md) before changing the display config or auth
   code.** Most of the hard bugs here are non-obvious (RGB 18-bit pixel mode, the
   TCA9554 expander off-by-one, the nested Claude token, the Codex account header).
   Don't rediscover them.
2. **The ESP32 holds no secrets.** All credentials stay host-side in the daemon. Keep
   it that way.
3. **Never commit secrets.** `firmware/secrets.yaml` (WiFi) and `daemon/.env` (MQTT +
   creds) are gitignored. Verify with `git check-ignore` before committing.
4. **Hardware facts come from the vendor demo**, not from guessing or from training
   priors — `docs/reference/waveshare-demo/Display_ST7701.cpp` etc.

## How to work on each part

### Daemon (`daemon/`)
- Build: `cd daemon && go build -o claude-monitor-daemon .`
- It runs as a systemd service. After changes:
  `go build ... && sudo systemctl restart claude-monitor`.
- Logs: `journalctl -u claude-monitor -f`.
- Config via `daemon/.env` (`MQTT_BROKER`, `MQTT_USER`, `MQTT_PASS`, `POLL_INTERVAL`).
- Adding a provider = follow the Codex pattern: a `fetchXUsage(ctx) *usage`, a
  `codexStateTopic`-style topic, discovery entries in `discoverySensors()`, and a
  branch in the `poll()` closure.
- You can test a usage fetch in isolation with a tiny throwaway `main` that prints
  only header/field values (never print tokens).

### Firmware (`firmware/claude-monitor.yaml`)
- Edit + flash via the ESPHome dashboard (Home Assistant add-on) → **Install →
  Wirelessly (OTA)**. First flash only is USB from Windows (see LEARNINGS).
- Validate YAML structure locally before flashing (PyYAML with `!secret`/`!lambda`
  treated as opaque). The ESPHome compiler is the real validator — expect it to flag
  unknown LVGL/display keys; fix iteratively.
- Turn on `logger: level: DEBUG` and `i2c: scan: true` when debugging the panel —
  the boot log shows whether 0x20 (expander) / 0x15 (touch) are found and whether the
  display inits.
- UI is an LVGL `tileview` (Claude / Codex / Settings tiles). HA sensor `on_value`
  handlers push values into the arcs/labels and recolor by threshold.

## Conventions

- The user (Mathieu) dislikes audible alerts — **no buzzer / sound**. High usage is
  signaled by color only (green <70, amber 70–90, red >90).
- Keep the two-arc layout: outer ring = weekly, inner ring = session.
- Reset times can show as a countdown ("2h39m") or wall-clock; controlled by the
  `reset_show_clock` global, toggled on the Settings tile.

## Things that are intentionally NOT here

- No BLE. (The reference Claudial used BLE; we use WiFi/MQTT for home+office reach.)
- No on-device credentials or API calls to Anthropic/OpenAI.
- No `ccusage`-style local-log parsing — we use the live rate-limit data instead.
