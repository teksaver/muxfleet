#!/usr/bin/env bash
set -euo pipefail

# Component: prj
# AD-22: binary permissions, polkit rules for systemctl template units, narrow rule support.

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

# 1. Ensure /usr/local/bin exists
if [ ! -d /usr/local/bin ]; then
  if mkdir -p /usr/local/bin < /dev/null 2>/dev/null; then
    CHANGED=$((CHANGED + 1))
  else
    record_failure "failed to create /usr/local/bin"
  fi
fi

# 2. Ensure /usr/local/bin/prj has 0755 if it exists
if [ -f /usr/local/bin/prj ] && ensure_mode /usr/local/bin/prj 0755; then
  CHANGED=$((CHANGED + 1))
fi

# 3. polkitd: the rules below only take effect with it, and some images ship without it (AD-13)
if ! dpkg-query -W -f='${Status}' polkitd 2>/dev/null | grep -q "install ok installed"; then
  if DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends polkitd < /dev/null >/dev/null 2>&1 \
    || { apt-get update -q < /dev/null >/dev/null 2>&1 \
      && DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends polkitd < /dev/null >/dev/null 2>&1; }; then
    CHANGED=$((CHANGED + 1))
  else
    record_failure "failed to install polkitd"
  fi
fi

# 4. Polkit rule for muxfleet units
# AD-13: allow dev to start/restart muxfleet-* units
POLKIT_DIR="/etc/polkit-1/rules.d"
POLKIT_RULE="$POLKIT_DIR/50-muxfleet.rules"

WANT_POLKIT='// Managed by muxfleet (prj component)
polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units") {
        var unit = action.lookup("unit");
        var verb = action.lookup("verb");
        if (unit && unit.indexOf("muxfleet-") === 0 && (verb === "start" || verb === "restart")) {
            if (subject.user === "dev") {
                return polkit.Result.YES;
            }
        }
    }
});
'

if [ -d "$POLKIT_DIR" ]; then
  if write_if_changed "$POLKIT_RULE" "$WANT_POLKIT" "0644"; then
    CHANGED=$((CHANGED + 1))
  fi
else
  record_failure "missing $POLKIT_DIR: polkit rule not installed"
fi

# Format failures list
FAILURES_STR="[]"
if [ ${#FAILURES[@]} -gt 0 ]; then
  IFS=','
  FAILURES_STR="[${FAILURES[*]}]"
fi

# Render JSON output
echo "{\"component\": \"prj\", \"changed\": $CHANGED, \"failures\": $FAILURES_STR}"
