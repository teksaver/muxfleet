#!/usr/bin/env bash
set -euo pipefail

# Component: reboot
# AD-22: reboot unit and polkit rule permitted to user 'dev'.

CHANGED=0
FAILURES=()

# record_failure adds a message to the JSON failures list, quotes and backslashes escaped.
record_failure() {
  local msg="${1//\\/\\\\}"
  msg="${msg//\"/\\\"}"
  FAILURES+=("\"$msg\"")
}

# perm_of prints the octal mode of a path (GNU stat, BSD stat as a fallback).
perm_of() {
  stat -c "%a" "$1" 2>/dev/null || stat -f "%OLp" "$1" 2>/dev/null || true
}

# The helpers below are called as `if` conditions, where bash ignores `set -e`, so they check
# every command themselves (AD-22: report changes and failures). Each returns 0 when it changed
# the system and read the result back, 1 when nothing had to change, and 2 when it had to change
# but could not: the failure is recorded and the step must not be counted.

# write_if_changed makes target hold content with mode, through a temporary file renamed in place.
write_if_changed() {
  local target="$1" content="$2" mode="${3:-0644}" tmp
  if [ -f "$target" ] && printf "%s" "$content" | cmp -s - "$target" && [ "$(perm_of "$target")" = "${mode#0}" ]; then
    return 1
  fi
  if [ -d "$target" ]; then
    record_failure "$target: is a directory, file not written"
    return 2
  fi
  if ! tmp=$(mktemp "$(dirname "$target")/.muxfleet-tmp.XXXXXX" 2>/dev/null); then
    record_failure "$target: cannot create a temporary file next to it, file not written"
    return 2
  fi
  if printf "%s" "$content" > "$tmp" && chmod "$mode" "$tmp" 2>/dev/null && mv -f "$tmp" "$target" 2>/dev/null \
    && printf "%s" "$content" | cmp -s - "$target" && [ "$(perm_of "$target")" = "${mode#0}" ]; then
    return 0
  fi
  rm -f "$tmp"
  record_failure "$target: write failed or not read back as expected"
  return 2
}

# ensure_mode sets path to mode when it differs, then reads it back.
ensure_mode() {
  local path="$1" mode="$2"
  if [ "$(perm_of "$path")" = "${mode#0}" ]; then
    return 1
  fi
  if chmod "$mode" "$path" < /dev/null 2>/dev/null && [ "$(perm_of "$path")" = "${mode#0}" ]; then
    return 0
  fi
  record_failure "$path: chmod $mode failed"
  return 2
}

# 1. Systemd unit muxfleet-reboot.service
SYSTEMD_DIR="/etc/systemd/system"
REBOOT_UNIT="$SYSTEMD_DIR/muxfleet-reboot.service"

WANT_UNIT='[Unit]
Description=Muxfleet Scheduled Reboot
DefaultDependencies=no
After=shutdown.target

[Service]
Type=oneshot
ExecStart=/bin/systemctl --message="muxfleet triggered reboot" reboot
'

if [ -d "$SYSTEMD_DIR" ]; then
  if write_if_changed "$REBOOT_UNIT" "$WANT_UNIT" "0644"; then
    CHANGED=$((CHANGED + 1))
    # Reported on this run only: the next run finds the unit file in place.
    systemctl daemon-reload >/dev/null 2>&1 < /dev/null \
      || record_failure "systemctl daemon-reload failed: $REBOOT_UNIT written but not loaded"
  fi
fi

# 2. Polkit rule to allow dev to trigger reboot unit
POLKIT_DIR="/etc/polkit-1/rules.d"
POLKIT_RULE="$POLKIT_DIR/50-muxfleet-reboot.rules"

WANT_POLKIT='// Managed by muxfleet (reboot component)
polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units") {
        if (action.lookup("unit") == "muxfleet-reboot.service" && action.lookup("verb") == "start") {
            if (subject.user === "dev") {
                return polkit.Result.YES;
            }
        }
    }
});
'

# polkitd is installed by the prj component; without it the rule would be silently inert
if [ -d "$POLKIT_DIR" ]; then
  if write_if_changed "$POLKIT_RULE" "$WANT_POLKIT" "0644"; then
    CHANGED=$((CHANGED + 1))
  fi
else
  record_failure "missing $POLKIT_DIR: polkit rule not installed (apply the prj component first)"
fi

# Format failures list
FAILURES_STR="[]"
if [ ${#FAILURES[@]} -gt 0 ]; then
  IFS=','
  FAILURES_STR="[${FAILURES[*]}]"
fi

# Render JSON output
echo "{\"component\": \"reboot\", \"changed\": $CHANGED, \"failures\": $FAILURES_STR}"
