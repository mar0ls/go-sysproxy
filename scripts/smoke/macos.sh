#!/usr/bin/env bash
# macOS smoke test. Changes the proxy of every network service and clears it at
# the end; refuses to run when a proxy is already configured (FORCE=1 overrides).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$HERE/lib.sh"

BIN="${1:-./sysproxy}"
PROXY_URL="${PROXY_URL:-http://127.0.0.1:8080}"
PAC_URL="${PAC_URL:-https://example.com/proxy.pac}"
require_binary "$BIN"

# Every enabled service, whatever `get` reports.
services() { networksetup -listallnetworkservices | tail -n +2 | grep -v '^\*'; }

# no_proxy_left: no service has web, secure web, SOCKS or auto-proxy enabled.
no_proxy_left() {
  local svc kind
  while IFS= read -r svc; do
    for kind in -getwebproxy -getsecurewebproxy -getsocksfirewallproxy -getautoproxyurl; do
      if networksetup "$kind" "$svc" | grep -q '^Enabled: Yes'; then
        fail "'$svc' still has $kind enabled"
      fi
    done
  done < <(services)
  ok "no service has a proxy enabled"
}

smoke_scenario
no_proxy_left

echo "[macOS] done"
