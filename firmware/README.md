# Firmware (ESPHome)

The complete device firmware for the **Waveshare ESP32-S3-Touch-LCD-2.1**. One file,
`claude-monitor.yaml`, defines the display driver, touch, the LVGL UI, and the Home
Assistant sensor bindings that feed the gauges.

## What it shows

An LVGL `tileview` you swipe between:

1. **Claude** — outer ring = weekly %, inner ring = session %, big center %, reset
   countdown. Rings recolor green → amber (70%) → red (90%).
2. **Codex** — same layout, teal/gold rings.
3. **Settings** — backlight brightness slider, reset-time format toggle
   (countdown ↔ wall-clock).

## Setup

1. Copy `secrets.yaml.example` → `secrets.yaml` and set your WiFi:
   ```yaml
   wifi_ssid: "..."
   wifi_password: "..."
   ```
2. Add `claude-monitor.yaml` to the ESPHome dashboard (Home Assistant add-on).
3. **First flash only:** from a Windows browser (Chrome/Edge) at `web.esphome.io`,
   over USB — see [../LEARNINGS.md](../LEARNINGS.md#flash-from-windows-not-wsl).
4. After that: **Install → Wirelessly (OTA)**.

The device pulls its values from these HA sensors (published by the daemon):
`sensor.claude_monitor_claude_*` and `sensor.claude_monitor_codex_*`.

## Key config sections

| Section | Purpose |
|---------|---------|
| `esp32: framework: esp-idf` + `psram:` | RGB panel needs ESP-IDF + PSRAM framebuffers |
| `i2c:` + `pca9554:` | shared I²C bus + the TCA9554 expander (panel reset/CS) |
| `switch:` (EXIO8) | panel-enable line, driven low at boot |
| `spi:` (software) | 3-wire bus for the ST7701 init sequence only |
| `display: mipi_rgb` | ST7701 panel — pins, timings, `init_sequence`, `pixel_mode: 18bit` |
| `touchscreen: cst816` | CST820 touch (polling mode) |
| `sensor: homeassistant` | pulls usage values, updates arcs/labels, recolors |
| `lvgl: tileview` | the 3 swipeable tiles |

## Debugging the panel

If the screen is black or wrong-colored, turn on:

```yaml
logger:
  level: DEBUG
i2c:
  scan: true
```

The boot log then shows the I²C scan (expect `0x20` expander, `0x15` touch) and the
display init. **Before** changing anything, read
[../LEARNINGS.md](../LEARNINGS.md) — black screens and the "everything is green" bug
have specific, already-solved causes (expander pin off-by-one, EXIO8 enable,
`pixel_mode: 18bit`).

## Regenerating the ST7701 init sequence

The `init_sequence:` in the YAML was converted from the vendor demo's
`ST7701_Init()` in `docs/reference/waveshare-demo/LVGL_Arduino/Display_ST7701.cpp`.
If you ever need to regenerate it, parse the `ST7701_WriteCommand`/`WriteData` calls
into `[cmd, data, ...]` arrays (ESPHome auto-appends SLPOUT/PIXFMT/INVON/DISPON, so
those trailing commands can be trimmed).
