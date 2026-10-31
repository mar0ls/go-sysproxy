#!/usr/bin/env bash
# Shared helpers for the smoke scripts. Source it; it does not run anything.
# Every check either passes silently-ish ("ok: …") or aborts the script with a
# non-zero exit code, so a green run means every assertion held.

FAILED=0

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

ok() { echo "ok: $*"; }

# run <expected-exit-code> <command…>: runs the command, keeps stdout+stderr in
# $OUT and fails when the exit code differs.
run() {
  local want=$1
  shift
  local got=0
  OUT="$("$@" 2>&1)" || got=$?
  [[ $got -eq $want ]] || fail "'$*' exited $got, want $want. Output: $OUT"
  ok "'$*' → exit $want"
}

# contains <text>: $OUT must contain the literal text.
contains() {
  grep -qF -- "$1" <<<"$OUT" || fail "output lacks '$1'. Output: $OUT"
  ok "output contains '$1'"
}

# lacks <text>: $OUT must not contain the literal text.
lacks() {
  if grep -qF -- "$1" <<<"$OUT"; then fail "output unexpectedly contains '$1'. Output: $OUT"; fi
  ok "output does not contain '$1'"
}

# require_binary <path>
require_binary() {
  [[ -x "$1" ]] || fail "binary not found or not executable: $1 (build: go build -o sysproxy ./cmd/sysproxy)"
}

# The scenario shared by every Unix-like platform. It needs $BIN, $PROXY_URL and
# $PAC_URL, and leaves the system proxy cleared. Platform scripts add their own
# checks of the native settings around it.
#
# It refuses to start when a proxy is already configured, because it ends with
# `unset` and would erase the user's settings.
smoke_scenario() {
  run 0 "$BIN" version

  OUT="$("$BIN" get --json 2>&1)" && got=0 || got=$?
  if [[ $got -ne 2 && "${FORCE:-0}" != 1 ]]; then
    fail "a proxy is already configured (get exited $got): this script would erase it. Output: $OUT. Re-run with FORCE=1 to proceed anyway."
  fi
  trap '"$BIN" unset --scope global >/dev/null 2>&1 || true' EXIT

  echo "--- http proxy fills HTTP only"
  run 0 "$BIN" set "$PROXY_URL" --scope global
  run 0 "$BIN" get
  [[ "$OUT" == "$PROXY_URL" ]] || fail "get printed '$OUT', want '$PROXY_URL'"
  ok "get printed the proxy URL"
  run 0 "$BIN" get --json
  contains "\"http\": \"$PROXY_URL\""
  lacks '"socks"'

  echo "--- socks5 replaces it and fills SOCKS only"
  run 0 "$BIN" set socks5://127.0.0.1:1080 --scope global
  run 0 "$BIN" get --json
  contains '"socks": "socks5://127.0.0.1:1080"'
  lacks '"http"'
  lacks '"https"'

  echo "--- credentials never reach the output"
  run 0 "$BIN" set http://alice:s3cr3tpw@127.0.0.1:8080 --scope global
  lacks s3cr3tpw
  run 0 "$BIN" get --json
  lacks s3cr3tpw

  echo "--- PAC"
  run 0 "$BIN" pac "$PAC_URL" --scope global
  run 0 "$BIN" get --json
  contains "\"pac\": \"$PAC_URL\""

  echo "--- invalid input is refused"
  run 1 "$BIN" set 'not a url' --scope global
  run 1 "$BIN" pac 'ftp://example.com/p.pac' --scope global

  echo "--- unset leaves nothing behind"
  run 0 "$BIN" unset --scope global
  run 2 "$BIN" get
  run 2 "$BIN" get --json
}
