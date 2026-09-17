#!/usr/bin/env bash
# Schedule refresh.sh every five minutes as a user LaunchAgent (macOS).
#
# Usage: install-launchd.sh [--bindings FILE] [--out FILE] [--interval SECONDS] [--uninstall]
# Defaults: ~/.config/frost/capacity-bindings.json, ~/.config/frost/capacity.json, 300 s.
# Logs go to ~/Library/Logs/frost/capacity.log. The agent runs this
# repository's refresh.sh in place, so keep the checkout where it is or
# rerun this script after moving it.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
label="net.vorwaller.frost-capacity"
plist="$HOME/Library/LaunchAgents/$label.plist"
bindings="$HOME/.config/frost/capacity-bindings.json"
out="$HOME/.config/frost/capacity.json"
interval=300
uninstall=0
while [ $# -gt 0 ]; do
  case "$1" in
    --bindings) bindings="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    --interval) interval="$2"; shift 2 ;;
    --uninstall) uninstall=1; shift ;;
    -h|--help) sed -n '2,8p' "$0"; exit 0 ;;
    *) echo "install-launchd.sh: unknown argument $1" >&2; exit 2 ;;
  esac
done

domain="gui/$(id -u)"
if [ "$uninstall" -eq 1 ]; then
  launchctl bootout "$domain/$label" 2>/dev/null || true
  rm -f "$plist"
  echo "removed $label"
  exit 0
fi

[ -f "$bindings" ] || { echo "install-launchd.sh: bindings $bindings not found; copy examples/capacity/bindings.json there first" >&2; exit 2; }
logdir="$HOME/Library/Logs/frost"
mkdir -p "$logdir" "$(dirname "$plist")"

# PATH for launchd is minimal; include the usual homes of codexbar, jq, frost.
path_value="/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:/usr/bin:/bin"
cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>$here/refresh.sh</string>
    <string>--bindings</string><string>$bindings</string>
    <string>--out</string><string>$out</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>$path_value</string></dict>
  <key>StartInterval</key><integer>$interval</integer>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>$logdir/capacity.log</string>
  <key>StandardErrorPath</key><string>$logdir/capacity.log</string>
</dict>
</plist>
PLIST

launchctl bootout "$domain/$label" 2>/dev/null || true
launchctl bootstrap "$domain" "$plist"
launchctl kickstart -k "$domain/$label"
echo "installed $label ($plist), every $interval s, log $logdir/capacity.log"
