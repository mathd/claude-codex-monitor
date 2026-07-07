#!/usr/bin/env bash
# ===================================================================
# Pull the claude-monitor ESPHome firmware from GitHub into the HA
# ESPHome config dir. Run from the HA "core-ssh" terminal:
#
#   cd /config/esphome
#   wget -O update.sh https://raw.githubusercontent.com/mathd/claude-codex-monitor/main/firmware/update.sh
#   bash update.sh
#
# Then flash from the ESPHome dashboard: Install -> Wirelessly (OTA).
#
# Idempotent: overwrites the tracked files, creates the boards/common
# subdirs, and NEVER touches secrets.yaml (your WiFi creds stay local).
# ===================================================================
set -euo pipefail

BASE="${BASE:-https://raw.githubusercontent.com/mathd/claude-codex-monitor/main/firmware}"

# Files to pull, relative to the firmware/ dir. Mirrors the !include graph
# in claude-monitor.yaml — keep in sync when adding a package/board.
FILES=(
  claude-monitor.yaml
  common/core.yaml
  common/ui.yaml
  common/transport_ha.yaml
  boards/lcd21.yaml
)

echo "Updating from $BASE"

# Create the subdirs the files land in (boards/, common/).
for f in "${FILES[@]}"; do
  dir="$(dirname "$f")"
  [ "$dir" = "." ] || mkdir -p "$dir"
done

fail=0
for f in "${FILES[@]}"; do
  # -O to the exact relative path (this is the bug in the manual flow:
  # boards/lcd21.yaml must NOT be saved as common/lcd21.yaml).
  if wget -q -O "$f" "$BASE/$f"; then
    echo "  ok   $f"
  else
    echo "  FAIL $f  (HTTP error — left previous copy if any)"
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo
  echo "One or more files failed to download. Nothing was flashed."
  echo "Check the path exists in the repo, then re-run."
  exit 1
fi

if [ ! -f secrets.yaml ]; then
  echo
  echo "NOTE: secrets.yaml is missing. Create it with your WiFi creds"
  echo "before flashing (it is intentionally NOT pulled from GitHub)."
fi

echo
echo "Done. Flash from the ESPHome dashboard: Install -> Wirelessly (OTA)."
