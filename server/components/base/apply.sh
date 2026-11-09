#!/usr/bin/env bash
set -euo pipefail

# Component: base
# AD-22: dev, admin access, SSH hardening, root password lock, metadata reservation,
# swap (swappiness=10), unattended-upgrades, dpkg lock timeout, hidepid, common foundation.

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

# 1. Ensure /etc/muxfleet exists with 0700 (AD-10) and runtime dirs exist
if [ ! -d /etc/muxfleet ]; then
  if mkdir -p -m 0700 /etc/muxfleet < /dev/null 2>/dev/null; then
    CHANGED=$((CHANGED + 1))
  else
    record_failure "failed to create /etc/muxfleet"
  fi
fi
if [ -d /etc/muxfleet ] && ensure_mode /etc/muxfleet 0700; then
  CHANGED=$((CHANGED + 1))
fi

for dir in /var/lib/muxfleet/status /run/muxfleet/locks; do
  if [ ! -d "$dir" ]; then
    if mkdir -p "$dir" < /dev/null 2>/dev/null; then
      CHANGED=$((CHANGED + 1))
    else
      record_failure "failed to create $dir"
    fi
  fi
done

# 2. User 'dev'
if ! id -u dev >/dev/null 2>&1; then
  useradd -m -s /bin/bash -u 1000 dev < /dev/null 2>/dev/null || true
  if id -u dev >/dev/null 2>&1; then
    CHANGED=$((CHANGED + 1))
  else
    record_failure "failed to create user dev"
  fi
fi

# Ensure dev home permissions (umask 077 -> 0700)
if [ -d /home/dev ] && ensure_mode /home/dev 0700; then
  CHANGED=$((CHANGED + 1))
fi

# 3. Admin access: /root/.ssh permissions
if [ -d /root/.ssh ] && ensure_mode /root/.ssh 0700; then
  CHANGED=$((CHANGED + 1))
fi
if [ -f /root/.ssh/authorized_keys ] && ensure_mode /root/.ssh/authorized_keys 0600; then
  CHANGED=$((CHANGED + 1))
fi

# 4. SSH hardening configuration
SSHD_CONF_DIR="/etc/ssh/sshd_config.d"
SSHD_MUX_CONF="$SSHD_CONF_DIR/50-muxfleet.conf"
WANT_SSHD_CONF="# Managed by muxfleet (base component)
PasswordAuthentication no
PermitRootLogin prohibit-password
AllowUsers dev root
"

if [ -d "$SSHD_CONF_DIR" ]; then
  if write_if_changed "$SSHD_MUX_CONF" "$WANT_SSHD_CONF" "0644"; then
    CHANGED=$((CHANGED + 1))
    # ssh is socket-activated on 26.04: a running sshd must reload to apply the file, a stopped
    # one reads it when it starts. Never reload a configuration that sshd itself refuses.
    # A failed reload is reported on this run only: the next run finds the file in place.
    SSH_STATE=$(systemctl is-active ssh < /dev/null 2>/dev/null || true)
    if ! sshd -t < /dev/null >/dev/null 2>&1; then
      record_failure "sshd -t refuses the configuration: ssh not reloaded"
    elif [ "$SSH_STATE" = "active" ]; then
      systemctl reload ssh >/dev/null 2>&1 < /dev/null \
        || record_failure "ssh reload failed: $SSHD_MUX_CONF written but not in effect"
    elif [ "$SSH_STATE" != "inactive" ] && [ "$SSH_STATE" != "failed" ]; then
      record_failure "ssh state unreadable (systemctl is-active: '$SSH_STATE'): ssh not reloaded"
    fi
  fi
fi

# 5. Lock root password
if command -v passwd >/dev/null 2>&1; then
  PW_STATUS=$(passwd -S root 2>/dev/null | awk '{print $2}' || true)
  if [ -z "$PW_STATUS" ]; then
    record_failure "root password status unreadable (passwd -S root)"
  elif [ "$PW_STATUS" != "L" ] && [ "$PW_STATUS" != "LK" ]; then
    passwd -l root >/dev/null 2>&1 < /dev/null || true
    PW_STATUS=$(passwd -S root 2>/dev/null | awk '{print $2}' || true)
    if [ "$PW_STATUS" = "L" ] || [ "$PW_STATUS" = "LK" ]; then
      CHANGED=$((CHANGED + 1))
    else
      record_failure "failed to lock root password"
    fi
  fi
fi

# 6. Sysctl swap swappiness = 10
SYSCTL_FILE="/etc/sysctl.d/99-muxfleet-swappiness.conf"
WANT_SYSCTL="vm.swappiness = 10
"

if [ -d /etc/sysctl.d ]; then
  if write_if_changed "$SYSCTL_FILE" "$WANT_SYSCTL" "0644"; then
    CHANGED=$((CHANGED + 1))
  fi
  # The running value is loaded from the file only once the file holds the wanted value.
  if printf "%s" "$WANT_SYSCTL" | cmp -s - "$SYSCTL_FILE" && [ "$(sysctl -n vm.swappiness 2>/dev/null)" != "10" ]; then
    if sysctl -p "$SYSCTL_FILE" >/dev/null 2>&1 < /dev/null && [ "$(sysctl -n vm.swappiness 2>/dev/null)" = "10" ]; then
      CHANGED=$((CHANGED + 1))
    else
      record_failure "vm.swappiness: sysctl -p $SYSCTL_FILE did not set 10"
    fi
  fi
fi

# 7. DPkg lock timeout
DPKG_CONF="/etc/apt/apt.conf.d/99muxfleet-dpkg-lock"
WANT_DPKG='DPkg::Lock::Timeout "60";
'

if [ -d /etc/apt/apt.conf.d ]; then
  if write_if_changed "$DPKG_CONF" "$WANT_DPKG" "0644"; then
    CHANGED=$((CHANGED + 1))
  fi
fi

# 8. Unattended upgrades configuration (no auto-reboot)
UNATTENDED_CONF="/etc/apt/apt.conf.d/50muxfleet-unattended-upgrades"
WANT_UNATTENDED='// Managed by muxfleet (base component)
Unattended-Upgrade::Automatic-Reboot "false";
'

if [ -d /etc/apt/apt.conf.d ]; then
  if write_if_changed "$UNATTENDED_CONF" "$WANT_UNATTENDED" "0644"; then
    CHANGED=$((CHANGED + 1))
  fi
fi

# Format failures list
FAILURES_STR="[]"
if [ ${#FAILURES[@]} -gt 0 ]; then
  IFS=','
  FAILURES_STR="[${FAILURES[*]}]"
fi

# Render JSON output
echo "{\"component\": \"base\", \"changed\": $CHANGED, \"failures\": $FAILURES_STR}"
