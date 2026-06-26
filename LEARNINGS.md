# LEARNINGS

Non-obvious facts that cost real debugging time. Read this before touching the
display config or the daemon's auth code.

## Hardware: Waveshare ESP32-S3-Touch-LCD-2.1

### The display is RGB-parallel, not SPI
The ST7701S panel takes pixel data over a **16-bit RGB-parallel bus** (HSYNC/VSYNC/
DE/PCLK + 16 data lines). SPI (GPIO2 clk / GPIO1 mosi) is used **only** to send the
init command sequence. This needs ESP-IDF + PSRAM framebuffers — not the Arduino
framework. In ESPHome: `display: platform: mipi_rgb`.

### Reset and CS are behind a TCA9554 I/O expander (the #1 gotcha)
There is no direct GPIO for the panel's reset or chip-select. They hang off a
**TCA9554PWR** I²C expander at address **0x20**. A naive config that sets
`reset_pin: GPIOxx` silently fails → black screen.

**Off-by-one trap:** the vendor demo's `Set_EXIO(Pin, ...)` writes to **bit (Pin-1)**.
So the demo's `EXIO_PIN1` = hardware **bit 0**, `EXIO_PIN3` = **bit 2**, `EXIO_PIN8`
= **bit 7**. ESPHome's `pca9554` `number:` is the **raw bit**. Therefore:

| Signal | Vendor demo | ESPHome `number:` |
|--------|-------------|-------------------|
| Panel reset | EXIO1 | **0** (not 1!) |
| Panel CS | EXIO3 | **2** (not 3!) |
| Panel enable | EXIO8 | **7** |

### EXIO8 must be driven LOW at boot
The demo does `Set_EXIO(EXIO_PIN8, Low)` in `Driver_Init()` **before** backlight/LCD
init. This is a panel-enable line. Miss it → black screen even with a perfect init
sequence. In ESPHome it's a `switch: gpio` on expander pin 7 with `restore_mode: ALWAYS_OFF`.

### Black rendered as GREEN → pixel_mode mismatch (NOT a background bug)
Symptom: the whole panel glows green where it should be black; rings/text vanish into
it. This is **not** an LVGL background, page, or arc issue (we chased all three —
red herrings). The vendor init sets the panel to **RGB666 / 18-bit** (`0x3A, 0x66`),
but ESPHome `mipi_rgb` defaults to **16-bit**. The bit misalignment leaks "off"
pixels into the green channel.

**Fix:** on the display, set `pixel_mode: 18bit` + `color_order: rgb` +
`invert_colors: false`. (The panel MADCTL is RGB via `0x36, 0x00`; it uses INVOFF.)

### Backlight is a real GPIO
GPIO6, PWM via LEDC, active-high. Unlike reset/CS it is NOT on the expander.

### Touch is CST820 (a CST816 variant), and its interrupt is unreliable
I²C address 0x15, INT on GPIO16. In ESPHome use `touchscreen: platform: cst816`,
but **poll** (`update_interval: 50ms`) rather than rely on `interrupt_pin` — the
CST820 IRQ line is known-flaky in ESPHome. Some variants also need `skip_probe: true`.

### Authoritative pins + init sequence live in the vendor demo
The wiki's RGB pin table is incomplete (those pins are internal, board-to-panel, not
broken out). The real source of truth is the official demo at
`docs/reference/waveshare-demo/` — `Display_ST7701.cpp` (init sequence + RGB pins),
`TCA9554PWR.cpp` (expander), `Touch_CST820.h` (touch). It downloads from the **files
CDN** (`files.waveshare.com/.../ESP32-S3-Touch-LCD-2.1-Code.zip`), which is NOT
blocked the way the wiki HTML is.

## Data sources

### There is no "remaining quota" API
Neither Claude nor Codex subscription plans expose remaining quota directly. We read
it from the side channels both vendors' own clients use.

### Claude: the free OAuth usage endpoint (preferred)
`GET https://api.anthropic.com/api/oauth/usage` with `Authorization: Bearer <token>`,
`anthropic-beta: oauth-2025-04-20`, and a `claude-code/*` user-agent. This is a
**read-only, free** endpoint (no inference, no cost, can't trip a rate limit).
Returns `five_hour` (session), `seven_day` (weekly), and `seven_day_sonnet` (a
separate Sonnet weekly cap) — each with `utilization` (already a percent) and
`resets_at` (RFC3339). Also returns `extra_usage`/`spend` (credit balance).

Token from `~/.claude/.credentials.json`, **nested**: read
`claudeAiOauth.accessToken` specifically — NOT the first `accessToken` you find
(`mcpOAuth.*.accessToken` is a different MCP token). Refresh via
`POST https://platform.claude.com/v1/oauth/token` (JSON body
`grant_type=refresh_token&refresh_token&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&scope=...`),
then write the rotated tokens back so the Claude CLI stays in sync.

> History: we originally read rate-limit **response headers** off a tiny throwaway
> Haiku call (`anthropic-ratelimit-unified-5h-utilization` etc.). That worked but
> cost ~$0.02/day and could trip a rate limit. The usage endpoint (how OpenUsage
> does it) is strictly better — free, richer, self-refreshing.

### Codex: OAuth refresh + wham/usage endpoint
From `~/.codex/auth.json` read `tokens.refresh_token` + `tokens.account_id`, then:
1. **Refresh** (the access token expires fast): `POST https://auth.openai.com/oauth/token`,
   form body `grant_type=refresh_token&client_id=app_EMoamEEZ73f0CkXaXp7hrann&refresh_token=...`.
   Returns a fresh `access_token` (and rotates the refresh token — write it back to
   auth.json so the Codex CLI stays in sync).
2. **Usage**: `GET https://chatgpt.com/backend-api/wham/usage` with
   `Authorization: Bearer <access_token>` AND `ChatGPT-Account-Id: <account_id>`.
   **The account header is required** — without it you get 401.

Response shape (same idea as Claude): `rate_limit.primary_window.used_percent /
reset_after_seconds` (the 5h session) and `rate_limit.secondary_window.*` (weekly).

Because the daemon refreshes the token itself, the Codex tile shows live data even if
you haven't run Codex recently. Method reverse-engineered from OpenUsage
(`Sources/OpenUsage/Providers/Codex/CodexUsageClient.swift`).

### The weekly numbers are real (not estimates)
Both providers return the real weekly window, so we don't have to estimate it the way
a pure `ccusage`-log approach would.

## Tooling / environment

### Flash from Windows, not WSL
USB serial does not pass through to WSL2 without `usbipd-win` fiddling. Flash the
first time from **Windows** via `web.esphome.io` (Chrome/Edge — needs Web Serial),
selecting the **"USB Single Serial"** port (the CH343, COMx). After the first flash,
everything is OTA over WiFi. The web flasher's "TOTA" entry is ESPHome's OTA
pseudo-target, not your board.

### The daemon persists via systemd in WSL
systemd is enabled in this WSL (`/etc/wsl.conf` has `systemd=true`). The daemon runs
as `claude-monitor.service`. After rebuilding the binary:
`sudo systemctl restart claude-monitor`. Note WSL only runs while Windows keeps it
up; for hands-free start at logon, add a Task Scheduler entry running `wsl.exe`.

### HA prefixes MQTT entity IDs with the device name
The daemon's discovery names a sensor `claude_session_usage`, but Home Assistant
creates the entity as `sensor.claude_monitor_claude_session_usage` (device name +
object id). The ESPHome `homeassistant` sensor `entity_id:` must use the **full
prefixed** name.

### LVGL arc gotchas
- An arc's **track** is drawn between `start_angle` and `end_angle`. Setting them
  equal (e.g. both 270) draws **nothing** — the track disappears. Use `0`→`360` for a
  full-circle track; the indicator fills based on `value`.
- The whole-screen background is the LVGL `bottom_layer`, not just the page bg.
  For a tileview, also set each **tile**'s bg (and the tileview's) opaque — a
  per-arc `bg_color` only covers the area under the arcs, leaving the center white.
- **Don't call raw `lv_*` functions on a widget's `->obj` from a lambda.** ESPHome's
  lambda context only forward-declares `lv_obj_t` (incomplete type) → compile error
  "invalid use of incomplete type". Use the ESPHome **actions** instead, e.g.
  `lvgl.tileview.select: {id, row, column}` (column accepts a `!lambda`) rather than
  `lv_tileview_set_tile_by_index(id(tv)->obj, ...)`.
- A `text: !lambda` that returns a **ternary of two string literals**
  (`cond ? "A" : "B"`) fails to compile: the result is `const char*`, and ESPHome
  calls `.c_str()` on it. Wrap a branch in `std::string(...)` so the expression's type
  is `std::string`. (A `static char b[]; snprintf(...); return b;` lambda is fine —
  ESPHome has a `const char*` overload; only the bare-literal ternary trips it.)
