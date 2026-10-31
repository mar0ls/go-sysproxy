#!/usr/bin/env bash
# Linux desktop smoke test (GNOME or KDE session). Changes the desktop proxy
# and clears it at the end; refuses to run when a proxy is already configured
# (FORCE=1 overrides).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

BIN="${1:-./sysproxy}"
PROXY_URL="${PROXY_URL:-http://127.0.0.1:8080}"
PAC_URL="${PAC_URL:-https://example.com/proxy.pac}"
require_binary "$BIN"

gnome=0
if command -v gsettings >/dev/null 2>&1 && gsettings list-keys org.gnome.system.proxy >/dev/null 2>&1; then
  gnome=1
fi
kde=0
if command -v kwriteconfig6 >/dev/null 2>&1 || command -v kwriteconfig5 >/dev/null 2>&1; then
  kde=1
fi
[[ $gnome -eq 1 || $kde -eq 1 ]] || fail "neither the GNOME proxy schema nor kwriteconfig5/6 is available: nothing to test"

smoke_scenario

# The desktop's own settings must agree with what the CLI reported.
if [[ $gnome -eq 1 ]]; then
  gs() { gsettings get "$@"; }
  expect_gs() { # expect_gs <schema> <key> <value>
    local got
    got="$(gs "$1" "$2")"
    [[ "$got" == "$3" ]] || fail "gsettings $1 $2 = $got, want $3"
    ok "gsettings $1 $2 = $3"
  }
  expect_gs org.gnome.system.proxy mode "'none'"

  run 0 "$BIN" set "$PROXY_URL" --scope global
  expect_gs org.gnome.system.proxy mode "'manual'"
  expect_gs org.gnome.system.proxy.http host "'127.0.0.1'"
  expect_gs org.gnome.system.proxy.http port 8080
  run 0 "$BIN" set socks5://127.0.0.1:1080 --scope global
  expect_gs org.gnome.system.proxy.socks port 1080
  run 0 "$BIN" pac "$PAC_URL" --scope global
  expect_gs org.gnome.system.proxy mode "'auto'"
  expect_gs org.gnome.system.proxy autoconfig-url "'$PAC_URL'"
  run 0 "$BIN" unset --scope global
  expect_gs org.gnome.system.proxy mode "'none'"
fi

echo "[Linux] done"
