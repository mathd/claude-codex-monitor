# v2 Plan — One Codebase, Two Boards, Two Transports

**Goal.** Keep the existing 2.1 board as the home unit (Home Assistant/MQTT, exactly as
today) and add a second device, the ESP32-S3-Touch-LCD-1.85B, that works **both** via
HA/MQTT at home and via **BLE pushed from the MacBook Pro at the office** (no HA there,
and the device cannot join the corporate network).

**Non-goals.** No fork. No on-device credentials — the Mac computes/fetches everything
and pushes only display values (percentages, minutes), preserving the "ESP32 holds no
secrets" rule. No buzzer/sound (unchanged).

**Constraint that shapes everything:** ESPHome compiles one image per device, and the
two boards differ in display driver (ST7701 RGB vs ST77916 QSPI), touch (CST820 vs
CST816), resolution (480×480 vs 360×360), pins, and the TCA9554 expander (2.1 only).
So: **shared source via ESPHome packages, two thin device YAMLs, two builds.**
BLE is only ever compiled into the 1.85B image — the 2.1 pays nothing for it and keeps
its hard-won RGB timing untouched.

---

## Target file layout

```
firmware/
  claude-monitor.yaml          # device: 2.1 board — substitutions + packages (lcd21, ui, transport_ha).
                               #   Name kept (NOT renamed to -21): the ESPHome dashboard entry,
                               #   OTA target, and HA device name all key off it.
  claude-monitor-185b.yaml     # device: 1.85B  — substitutions + packages (lcd185b, ui, transport_ha, transport_ble)
  common/
    core.yaml                  # esphome/esp32/psram/logger/wifi/api/ota/time — shared skeleton
    ui.yaml                    # globals, scripts, LVGL tileview, fonts, images, intervals (clock + auto-switch)
    transport_ha.yaml          # the homeassistant: sensors → call shared scripts
    transport_ble.yaml         # esp32_ble_server GATT service → call the same scripts
  boards/
    lcd21.yaml                 # i2c, pca9554, panel enable switch, init SPI, ST7701 display, CST820 touch, backlight
    lcd185b.yaml               # QSPI ST77916 display, CST816 touch, backlight (pins from the 1.85B wiki — vendor truth)
  secrets.yaml                 # unchanged, gitignored
daemon/                        # one Go module, gains a BLE output mode (Phase 5)
```

Each device YAML is ~30 lines: `esphome: name:`, `substitutions:` (sizes/fonts/pins
that differ), and a `packages:` block of `!include`s. ESPHome merges packages, and
substitutions are plain text replacement so they work inside lambdas and font sizes.

---

## Phase 1 — Packages refactor on the 2.1 (no behavior change) — *do now, before buying anything*

The enabler for everything else. Two sub-steps:

### 1a. Route all data through shared scripts

Today each `homeassistant` sensor's `on_value` writes directly into LVGL widgets
(`firmware/claude-monitor.yaml:349-511`). Extract one parameterized `script:` per value
so any transport can feed the UI:

| Script id | Parameter | Replaces handler of |
|---|---|---|
| `set_claude_session` | `pct: int` (-1 = unavailable) | `ha_session_pct` |
| `set_claude_week`    | `pct: int` | `ha_week_pct` |
| `set_claude_reset`   | `minutes: int` (-1 = unavailable) | `ha_session_reset` |
| `set_codex_session`  | `pct: int` | `ha_codex_session_pct` |
| `set_codex_week`     | `pct: int` | `ha_codex_week_pct` |
| `set_codex_reset`    | `minutes: int` | `ha_codex_reset` |

Convention: the transport converts its raw input (float with NAN from HA, bytes from
BLE) to `int`, using **-1 for "unavailable"**; the script owns the `--%` rendering, the
threshold colors (green <70 / amber 70–90 / red >90), and updating the auto-switch
globals (`claude_session_pct`, `codex_session_pct`). The NAN→int UB guard stays in the
HA transport where NAN originates.

Also extract the shared threshold-color lambda (repeated 4×) into one place — either a
single script parameterized by arc id is awkward in ESPHome, so acceptable to keep the
color lambda per-script but identical.

### 1b. Split into packages

Cut the current monolith along its existing section boundaries into the layout above.
`core.yaml` = lines 14–53 area (esphome/esp32/psram/logger/wifi/api/ota/time);
`boards/lcd21.yaml` = i2c/pca9554/switch/output/light/spi/display/touchscreen;
`ui.yaml` = interval/globals/font/image/lvgl + the new scripts;
`transport_ha.yaml` = the six sensors, now thin wrappers calling scripts.

**Validation:** compiles in the ESPHome dashboard, OTA to the 2.1, confirm arcs/labels
update from HA, thresholds recolor, auto-switch and tap-nav still work, no new shimmer.
This phase is pure reorganization — any visible difference is a bug.

---

## Phase 2 — BLE GATT contract (design, no code)

One custom service, one characteristic per value. Per-value characteristics beat a JSON
blob: no parser on the device, trivial `on_write` handlers, and the Mac only writes what
changed.

**Service UUID:** `4C4D0000-4B1D-4C75-8E9A-5F0C0A75D1E0` ("LM" = LLM Monitor; any fixed
random base works — pick once, never change).

| Characteristic (offset on base UUID) | Type | Meaning |
|---|---|---|
| `...0001` claude_session_pct | uint8 (0–100, 255 = unavailable) | 5h session % |
| `...0002` claude_week_pct    | uint8 | weekly % |
| `...0003` claude_reset_min   | uint16 LE (0xFFFF = unavailable) | minutes to session reset |
| `...0004` codex_session_pct  | uint8 | |
| `...0005` codex_week_pct     | uint8 | |
| `...0006` codex_reset_min    | uint16 LE | |
| `...00FF` staleness guard    | uint8 heartbeat counter | Mac writes every poll; device zeroes the UI if no write for 3× poll interval (mirrors the MQTT availability/will behavior) |

All characteristics: write + read (read helps debugging with nRF Connect). Device name
in advertising: `claudial-185b`.

**Security posture:** the data is low-sensitivity (percentages), the device holds no
secrets, and writes can at worst repaint gauges. ESPHome's BLE server has limited
pairing/bonding control, so v2 ships open-write and this is accepted. If it ever
matters: cheap application-level fix is an extra "unlock" characteristic requiring a
shared constant before writes are honored. Recorded here so it's a decision, not an
oversight.

---

## Phase 3 — 1.85B bring-up (hardware arrives)

1. **Pull the pin table and init details from the Waveshare 1.85B wiki page first**
   and save a copy under `docs/reference/` — vendor truth, same rule as the 2.1 (the
   1.28's RST-pin gotcha proved forum pins wrong).
2. Write `boards/lcd185b.yaml`: `mipi_spi`/QSPI ST77916 display + CST816 touchscreen
   (both ESPHome-native), backlight. No expander, no RGB timing — that entire class of
   config disappears.
3. Create `claude-monitor-185b.yaml` with substitutions for the 360×360 scale:
   arc radius/width, font sizes, image scales for the clock face. Budget a real UI
   pass here — 480→360 is -25% linear; the Montserrat-72 numerals and baton images
   will need per-board size substitutions, not just a global shrink.
4. First flash over USB (from Windows, per the flashing workflow), then OTA.

**Validation:** boots, display inits, touch swipes tiles, HA transport works at home —
i.e., the 1.85B reaches full v1 feature parity before any BLE work starts.

---

## Phase 4 — Firmware BLE transport (`transport_ble.yaml`, 1.85B only)

1. Add `esp32_ble`, `esp32_ble_server` with the Phase 2 service; each characteristic's
   `on_write` decodes 1–2 bytes and calls the matching Phase 1 script. (Requires
   ESPHome ≥ 2024.6 for user-defined services; the add-on will be well past that.)
2. **WiFi must become optional:** `wifi: reboot_timeout: 0s` on the 1.85B (device
   must not reboot-loop at the office), and the auto-switch "HA offline → idle" logic
   (`api.connected` check in `ui.yaml`) must treat *either* transport as live —
   gate it on "no fresh data from any transport", using the staleness heartbeat.
3. Staleness watchdog: an `interval:` that zeroes the usage globals (idle → clock tile)
   when neither HA nor BLE has delivered data recently. This replaces/absorbs the
   current `api.connected` check.
4. Check the build report for flash/IRAM cost of the BLE stack; 16MB flash and the
   QSPI (non-RGB) display leave ample headroom, but verify rather than assume.

**Validation:** with WiFi credentials that can't connect (simulating the office),
device boots to the clock tile, stays up, and writing values from **nRF Connect on a
phone** updates the arcs. That tests the whole device side before any Mac code exists.

**Last-writer-wins** is the transport arbitration: no mode switch, no priority. At home
both may write (harmless, they agree); at the office only BLE does. Keep it dumb.

---

## Phase 5 — Mac side: BLE output mode in the Go daemon

The daemon already owns all the hard logic (nested Claude token, Codex account header,
refresh flows — `daemon/claude.go`, `daemon/codex.go`). Reuse it; never reimplement it.

1. Refactor `main()`'s poll loop so publishing is behind a small interface
   (`publisher.publish(provider, *usage)`) with the existing MQTT implementation.
2. Add `daemon/ble_darwin.go`: a BLE central using `tinygo.org/x/bluetooth`
   (CoreBluetooth-backed on macOS) — scan for `claudial-185b`, connect, write the
   characteristics per poll, bump the heartbeat, reconnect on drop.
3. Config: `TRANSPORT=mqtt|ble|auto` in `.env` (`auto` = try MQTT broker, fall back to
   BLE — right default for a laptop that moves between home and office).
4. Ship it as a **launchd LaunchAgent** on the Mac (`~/Library/LaunchAssistants` plist,
   `KeepAlive`), the macOS analog of the Pi's systemd unit.
5. macOS will prompt for **Bluetooth permission (TCC)** for the binary/terminal on
   first run — expected, one-time.

**Validation:** at home with MQTT stopped, daemon in `ble` mode drives the 1.85B
end-to-end. Then the real test at the office.

**Risk to retire early (before even Phase 3):** some MDM-managed MacBooks restrict
Bluetooth accessory pairing. Test at the office with any BLE gadget (or the 2.1 running
a throwaway BLE advertise sketch) before committing to the plan's office leg.

---

## Phase 6 — Docs and rules cleanup

- CLAUDE.md: retire the "No BLE" golden rule and the `firmware/claude-monitor.yaml`
  single-file description; document the packages layout and the two device targets.
- LEARNINGS.md: add BLE/1.85B findings as they emerge (same discipline as v1).
- README.md: architecture diagram gains the BLE path.
- Update the `v2-ble-mac-bridge` memory when phases complete.

---

## Order of work and current status

| # | Phase | Depends on | Status |
|---|---|---|---|
| 1 | Packages refactor + scripts (2.1) | — | **done 2026-07-01** — validated with `esphome config` (2026.6.4); resolved-config diff vs the monolith shows only the intended script indirection. Remaining: OTA to the 2.1 and eyeball the tiles. NOTE: the ESPHome dashboard needs the `common/` and `boards/` subdirectories copied alongside `claude-monitor.yaml` (includes are relative). |
| 2 | GATT contract | — | designed above; freeze when Phase 4 starts |
| R | Office-Mac BLE permission check | — | **do early — cheap, kills the plan if it fails** |
| 3 | 1.85B bring-up | board purchased | blocked on purchase |
| 4 | Firmware BLE transport | 1, 2, 3 | |
| 5 | Daemon BLE mode (Mac) | 2 (not 4 — nRF Connect decouples them) | can develop in parallel with 4 |
| 6 | Docs cleanup | as phases land | |

## Open questions

1. **Does the office Mac allow BLE accessory connections?** (Risk item R — test first.)
2. Exact 1.85B pin table and whether ESPHome's ST77916 support needs the `mipi_spi`
   or dedicated platform — resolve from the wiki + current ESPHome docs at Phase 3.
3. Whether the clock-face baton/numeral images scale acceptably to 360×360 or need
   re-exported assets.
