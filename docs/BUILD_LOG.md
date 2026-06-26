# Build Log

Phase history of the project. Architecture and current setup live in the top-level
[README](../README.md); the hard-won facts are in [LEARNINGS](../LEARNINGS.md).

| Phase | Status | Notes |
|-------|--------|-------|
| 0 — Hardware research | ✅ | Pulled the authoritative pins + ST7701 init sequence from the vendor demo (`docs/reference/`). The wiki's pin tables are incomplete; the demo ZIP from the files CDN is ground truth. |
| 1 — Light the panel | ✅ | ST7701 RGB-parallel + TCA9554 expander + init sequence. Bugs solved: expander pin off-by-one (reset bit 0, CS bit 2), missing EXIO8 enable, and `update_interval: never` (lambda never drew). |
| 2 — Touch | ✅ | CST820 via `cst816` platform, polling mode (its IRQ is unreliable in ESPHome). Drives the tileview swipe. |
| 3 — Daemon → MQTT/HA | ✅ | Go daemon reads Claude rate-limit headers, publishes with HA discovery. Token = `claudeAiOauth.accessToken`. Runs as a systemd service. |
| 4 — LVGL UI | ✅ | Two-arc gauge. Bugs solved: whole-panel-green = `pixel_mode: 18bit` mismatch (RGB666); no-rings = zero-length arc track (start==end angle). |
| 5 — Codex tile | ✅ | Daemon auto-refreshes the Codex OAuth token and reads `wham/usage`. Live data, no need to run Codex first. Third tile + 4 more HA sensors. |
| 6 — Settings tile | ✅ | On-device brightness slider + reset-format (countdown/clock) toggle, persisted. |
| 7 — Repo cleanup | ✅ | Monorepo layout (`firmware/` + `daemon/` + `docs/`), README/CLAUDE/LEARNINGS, dropped upstream Claudial cruft. |
| — 3D-printed case | ☐ | Future. A round-display stand from MakerWorld is a starting point. |

## Design decisions

- **No buzzer / sound** — high usage is signaled by color only (green/amber/red).
- **WiFi/MQTT, not BLE** — works at home (Home Assistant) and the office (any local
  broker), and lets ESPHome own the hard RGB-panel bring-up.
- **Host-side daemon** — keeps all credentials off the device; the ESP32 only reads
  HA sensors.
