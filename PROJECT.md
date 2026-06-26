# Claude Code Usage Monitor — Waveshare ESP32-S3-Touch-LCD-2.1

Physical desk monitor showing Claude Code **5-hour session %** and **weekly %** usage
with reset countdowns, on a 480×480 round touch LCD. Inspired by "Claudial".

## Architecture

```
PC daemon (reads Anthropic rate-limit headers)
   ├── MQTT  ──► office (no Home Assistant needed)
   └── HA push ──► home (history, automations)
        │
        ▼
ESP32-S3-Touch-LCD-2.1  (ESPHome + LVGL tileview)
   swipe between Claude / (future Codex) pages
```

- **Data source:** a local daemon makes a tiny Haiku API call every 60s and reads
  `anthropic-ratelimit-unified-5h-*` / `-7d-*` response headers. Real numbers, ~$0.02/day.
  Reuses logic from github.com/Moge800/Claudial (cloned in `daemon/`).
- **Transport:** MQTT (office) + Home Assistant (home). ESPHome speaks both.
- **Firmware:** ESPHome (`claude-monitor.yaml`). Display driver ported from Waveshare's
  official demo (`reference/waveshare-demo/`).

## Build phases

- [x] Phase 0 — gather authoritative hardware facts (done; see `reference/`)
- [x] **Phase 1 — light the screen** ✅ DONE 2026-06-26. Panel shows "It works!". ST7701 RGB + TCA9554 expander + init sequence all confirmed on hardware.
- [ ] Phase 2 — CST820 touch (I2C 0x15, INT GPIO16)
- [x] Phase 3 — daemon → MQTT + HA ✅ DONE 2026-06-26. Daemon runs in WSL, publishes real data (session 8%, weekly 3%) to broker 10.99.0.120, 4 HA sensors via discovery. Token = claudeAiOauth.accessToken. Still needs persistent autostart.
- [x] Phase 4 — LVGL two-arc UI ✅ DONE 2026-06-26. Shows live 8%/week 4%/reset 2h39m. Entity IDs are sensor.claude_monitor_claude_* (HA prefixes device name).
- [ ] Phase 5 — alerts (onboard buzzer), color thresholds, multi-page
- [ ] Phase 6 — 3D-printed case

## Phase 1 — how to flash

1. Put your WiFi in `secrets.yaml` (template already created).
2. In Home Assistant → **ESPHome add-on** (or `esphome` CLI), add `claude-monitor.yaml`.
3. Connect the board by **USB-C** (CH343P auto-download — no button holding needed).
4. Click **Install → Plug into this computer** (first flash via USB; later flashes OTA).
5. Expect: black screen → backlight on → green "It works!" + "ST7701 480x480".

## ⚠️ Things to verify on first `esphome config` / compile

The pins and ST7701 init sequence are verbatim from the vendor demo and are trusted.
The ESPHome **mipi_rgb key names** were taken from partial docs — if the compiler
complains, these are the likely culprits (check current esphome.io/components/display/mipi_rgb):

- `mipi_rgb` may want `model:` (e.g. `custom`) — added `dimensions:` instead; confirm.
- `clk_pin`/`mosi_pin` for the init SPI bus — exact key names may differ
  (could be under an `spi`/`spi_id` block, or `enable_pin`). Verify.
- `pclk_inverted` vs `pclk_idle_high` — demo uses non-inverted (`pclk_active_neg=false`).
- The `pca9554` component id reference syntax in `cs_pin`/`reset_pin`.
- If `mipi_rgb` isn't the right platform name on your ESPHome version, the alternative
  is `rpi_dpi_rgb` (older) — the keys are similar.

If first flash shows a **white/garbled** screen: init ran but timing/color order is off
→ tweak porches or swap `pclk_inverted`. If **black with backlight on**: init sequence or
CS/reset (expander pins) didn't take → check i2c scan finds 0x20.

## Hardware quick-ref (full details in agent memory)

| | |
|---|---|
| MCU | ESP32-S3R8, 8MB octal PSRAM, 16MB flash |
| Display | ST7701S, RGB-parallel 16-bit, 480×480 |
| Reset/CS | via TCA9554 expander (I2C 0x20): EXIO1=reset, EXIO3=CS |
| Backlight | GPIO6 PWM |
| Touch | CST820 (I2C 0x15, INT GPIO16) — native swipe gestures |
| I2C | SDA GPIO15, SCL GPIO7 |
| Buzzer | onboard (for alerts) |
| Flash | USB-C, CH343P auto-download |
