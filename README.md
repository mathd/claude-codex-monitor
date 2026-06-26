# Claude & Codex Usage Monitor

A physical desk monitor that shows your live **Claude Code** and **Codex** usage —
5-hour session %, weekly %, and reset countdowns — on a 480×480 round touch LCD.

Swipe between a **Claude** tile, a **Codex** tile, and a **Settings** tile.
Arc colors shift green → amber → red as you approach your limits. No sound.

Inspired by [Claudial](https://github.com/Moge800/Claudial); built for harder
hardware (RGB-parallel panel) with a different data path (MQTT + Home Assistant).

## Architecture

```
daemon/  (Go, runs on your PC / WSL)
  • reads ~/.claude + ~/.codex credentials
  • Claude:  tiny API call → rate-limit headers
  • Codex:   refresh OAuth → wham/usage endpoint
  • publishes to MQTT with HA auto-discovery
        │ MQTT
        ▼
  Home Assistant  (Mosquitto broker + sensors)
        │ ESPHome native API (LAN)
        ▼
firmware/  (ESPHome, on the ESP32-S3)
  • ST7701 480×480 round panel + CST820 touch
  • LVGL tileview: Claude / Codex / Settings
```

Why this shape: there is **no official "remaining quota" API** for the Claude or
Codex subscription plans. The real numbers come from rate-limit response headers
(Claude) and the `wham/usage` endpoint (Codex), which require account credentials —
so a host-side daemon does that and feeds the device. The ESP32 never holds any
secrets. See [LEARNINGS.md](LEARNINGS.md) for the details.

## Layout

| Path | What |
|------|------|
| [`firmware/`](firmware/) | ESPHome config for the device (display, touch, LVGL UI) |
| [`daemon/`](daemon/) | Go daemon: reads usage, publishes to MQTT |
| [`docs/hardware/`](docs/hardware/) | Board datasheet/manual PDF |
| [`docs/reference/`](docs/reference/) | Waveshare's official demo (authoritative pins + ST7701 init sequence) |
| [`LEARNINGS.md`](LEARNINGS.md) | Hard-won, non-obvious facts (read this before debugging) |
| [`CLAUDE.md`](CLAUDE.md) | Orientation for AI agents working in this repo |

## Hardware

**Waveshare ESP32-S3-Touch-LCD-2.1** — ESP32-S3R8 (8MB PSRAM, 16MB flash),
2.1" 480×480 round IPS, **ST7701S** driver over **RGB-parallel**, **CST820**
capacitive touch, **TCA9554** I/O expander, onboard IMU/RTC/buzzer.

## Quick start

1. **Daemon** (`daemon/`): copy `.env.example` → `.env`, set your MQTT broker +
   credentials, then run it (or install the systemd service). Requires `claude login`
   and, for the Codex tile, `~/.codex/auth.json`. See [daemon/README.md](daemon/README.md).
2. **Firmware** (`firmware/`): copy `secrets.yaml.example` → `secrets.yaml`, set
   WiFi, then flash with ESPHome. See [firmware/README.md](firmware/README.md).
3. The 8 sensors (Claude + Codex) auto-appear in Home Assistant; the device reads
   them over the ESPHome native API.

## Status

Working: Claude + Codex live data, touch/swipe tiles, on-device brightness +
reset-format settings, daemon as a persistent systemd service.

See [PROJECT.md](PROJECT.md) for the build phases and history.
