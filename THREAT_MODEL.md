# Threat model (STRIDE)

Scope: `github.com/mar0ls/go-sysproxy` — v0.5.x plus the fixes on the `dev` branch described below — the library and the `cmd/sysproxy` CLI.
Method: STRIDE applied per trust boundary / data flow. Status column is one of
**Mitigated**, **Partial**, **Open** (with a tracking ID from `REVIEW_FINDINGS.md`) or
**Accepted** (conscious residual risk, documented here).

Status as of the `dev` branch after fixes F-01…F-21 (quoting and input validation, managed blocks,
atomic writes, credential redaction, error propagation, exact `WithProxy` restore, URL scheme and
port handling, Windows WinINET/Credential Manager, KDE 6). Verified in CI on real Windows, Linux and
macOS runners and on a Debian 13 VM with Plasma 6.

Evidence level used in this document: *reproduced* (exercised in a test or script),
*code-read* (derived from reading the source, not executed). Everything marked
**Mitigated** below has a regression test; the Windows, Linux (dconf, KDE 5 and 6) and macOS
paths were also run against the real system stores.

## 1. What is being protected

| ID | Asset | Why it matters |
|----|-------|----------------|
| A1 | Integrity of the OS proxy configuration | Whoever controls it controls where the machine's traffic goes (interception, exfiltration, loss of connectivity). |
| A2 | Proxy credentials (`user:pass@`) | Reusable secrets; often corporate SSO or paid residential-proxy accounts. |
| A3 | User shell start-up files (`~/.bashrc`, `~/.zshrc`, `~/.profile`, `~/.bash_profile`, PowerShell profile) | Executed on every shell start — write access is persistent code execution. |
| A4 | `/etc/environment` (Linux) | System-wide, read at login by every user, world-readable. |
| A5 | Other tool configs: `~/.curlrc`, `~/.wgetrc`, `~/.gitconfig`, npm config, `pip.conf` | Persist proxy settings and credentials outside the OS store. |
| A6 | Windows Credential Manager entries | Targets are keyed by host name and can collide with unrelated credentials. |
| A7 | Host availability | A bad `/etc/environment`, a stuck proxy or a corrupted rc file can lock a user out or cut connectivity. |

## 2. Actors and trust assumptions

| Actor | Trust | Notes |
|-------|-------|-------|
| Library consumer (calling code) | Trusted, but **its inputs may not be**. Proxy URL, PAC URL and `NoProxy` frequently come from config files, environment, or a remote API. The library must not turn a data string into code. |
| CLI user | Trusted for their own account. Arguments are visible to other local users via the process list. |
| Other local user / unprivileged process | Untrusted. Can read world-readable files, watch the process list, plant binaries earlier in `PATH`. |
| Same-user malware | Out of scope for confidentiality of A2/A3 (it can already write the rc files); in scope for not making its job easier. |
| Network attacker | Untrusted. Can alter a PAC fetched over `http://`, sniff `http://` proxy credentials. |
| Proxy / PAC server operator | Untrusted for content; the tool deliberately sends the user's traffic through it. |
| Supply chain (CI actions, toolchain) | Partly trusted; the module has zero third-party dependencies. |

## 3. Data-flow overview

```
 caller input ─────────┐
 (URL, PAC, NoProxy,   │  [B1] validation
  argv for the CLI)    ▼
                ┌──────────────────┐   [B2] exec of external tools (no shell)
                │ go-sysproxy core │──────────────► networksetup / gsettings / kwriteconfig6|5 /
                │  parse + backends│                reg / Credential Manager API / git / npm
                └─┬──────┬─────┬───┘                         │
                  │      │     │                             ▼
         [B3] file│      │     │[B5] logger sink        OS proxy stores
         writes   │      │     ▼                        (SystemConfiguration, dconf,
                  │      │  caller-supplied Logger       kioslaverc, WinINet registry,
                  ▼      ▼                               Credential Manager)
  rc files, /etc/environment,   [B4] process env (os.Setenv)
  ~/.curlrc, ~/.wgetrc, pip.conf, PS profile
```

Boundaries: **B1** untrusted string → parsed value; **B2** our process → external binary
resolved from the system directories (see S1); **B3** our process → files shared with other programs/users;
**B4** our process → its own (and children's) environment; **B5** our process → an
arbitrary logging sink.

## 4. Controls that already exist

- External tools are started with `exec.Command(path, args...)` — never via `sh -c` /
  `cmd /c` — so metacharacters in arguments are not interpreted by a shell at the exec layer.
  System tools get an absolute path from `lookSystemTool` (system directories first, `PATH` only for an unprivileged process).
- `context.Context` on all long operations; timeouts in the CLI.
- `validateProxyURL` requires a scheme, host and a numeric 1–65535 port; `url.Parse`
  already rejects control characters (including newlines) in proxy URLs.
- User-scope files are created `0600`; Windows credentials go to Credential Manager, not the registry.
- `/etc/environment` permission errors are reported as `RequiresElevation` rather than hidden.
- Every value written to a shell rc file or PowerShell profile is quoted (`shellQuote`, `psQuote`); PAC URLs are parsed, and PAC/proxy URLs with whitespace or control characters and `NoProxy` values outside a host-name character set are rejected.
- Files the tool does not own are edited only inside a `# >>> go-sysproxy >>>` … `# <<<` block, only if they already exist, and are replaced atomically (mode, owner and symlinks preserved).
- Passwords are masked in log messages, error messages and CLI output; they are never written to `/etc/environment`.
- Zero third-party dependencies; `gosec`/`staticcheck`/`errcheck` in CI; five fuzz targets
  (`FuzzParse`, `FuzzValidateProxyURL`, `FuzzValidatePACURL`, `FuzzShellQuote`, `FuzzPSQuote`), run nightly.
- Integration tests and CLI smoke scripts run against the real system proxy stores on Windows,
  Linux and macOS runners.

## 5. STRIDE analysis

### S — Spoofing

| # | Threat | Where | Status |
|---|--------|-------|--------|
| S1 | **PATH hijack.** Tools used to be looked up by bare name; a binary planted earlier in `PATH` ran with the caller's privileges and received proxy credentials on its command line — worst case when the CLI runs via `sudo` with a user-controlled `PATH`. | B2, all backends | **Mitigated.** OS tools (`networksetup`, `route`, `gsettings`, `kwriteconfig`, `kreadconfig`, `reg`) come from the system directories; only an unprivileged process falls back to `PATH`, a root process reports `ErrToolMissing` (`sysexec.go`, tested with a hostile `PATH`). `git`/`npm` for `WriteAppConfig` are the user's own tools and still use `PATH`. |
| S2 | **Identity confusion under `sudo`.** `gsettings`/KDE and rc-file writes land in *root's* account, not the desktop user's, and the call reported plain success. | Linux backend, `os.UserHomeDir` | **Mitigated.** The call returns a non-critical warning naming `SUDO_USER` (desktop settings, `ScopeUser`, `WriteAppConfig` when `HOME` is not the caller's); `gsettings` read-back already rejects the no-session-bus case; a file root creates in a user's directory takes that directory's owner. The settings still land in root's account — the tool warns, it does not switch users. |
| S3 | The Credential Manager target is the host name only (no port), so a stored credential can be picked up by an unrelated service on the same host name. | Windows backend | **Mitigated — F-17.** Credentials are written through the API with a `go-sysproxy` marker; `Set` never overwrites an entry that is not ours and `Unset`/restore delete only ours (`TestIntegrationWindows_ProxyCredentials`). The target still has no port. |
| S4 | PAC URL over `http://` can be spoofed by an on-path attacker, who then controls routing for the whole machine. | `SetPAC` | **Partial** — scheme allow-list exists, but plain `http://` is accepted silently. Recommend a documented warning, optionally an `https`-only option. |

### T — Tampering

| # | Threat | Where | Status |
|---|--------|-------|--------|
| T1 | **Code injection into shell start-up files.** Previously proxy URL, PAC URL and `NoProxy` were spliced into `export x='…'` / `$env:x = '…'` lines without quoting; `http://h:80/';touch X;'` executed on the next shell start. *Reproduced before the fix.* | B1→B3, `rcfile_unix.go`, `sysproxy_windows.go` | **Mitigated — F-01.** Quoting plus validation; regression tests source the written file in `bash` and fuzz the quoters. PowerShell quoting is covered by unit and fuzz tests and by `TestIntegrationWindows_UserScopeProfile`, which dot-sources the profile in a real Windows PowerShell with hostile values. |
| T2 | **Destructive edits of files the tool does not own.** `Unset(ScopeUser)` used to delete every line containing `_proxy=` / `_PROXY=` (`git_proxy`, aliases, comments, the user's `NO_PROXY`); `/etc/environment` lost the admin's `NO_PROXY` on every `Set`. *Reproduced before the fix.* | B3 | **Mitigated — F-04, F-05.** Only the marked block and the exact unmarked lines written by ≤ 0.5 are removed; `Set` followed by `Unset` restores the file byte for byte (tested). |
| T3 | **Non-atomic read-modify-write** of `/etc/environment`, rc files, `.curlrc`, `pip.conf`. A crash, `ENOSPC` or two concurrent callers could truncate or interleave the file. | B3 | **Partial — F-05.** Writes are now temp file + `rename` (mode/owner/symlinks preserved) and read-modify-write cycles are serialised in-process. No cross-process lock: two *processes* editing the same file can still lose an update. |
| T4 | **Creating files that change shell behaviour.** `ScopeUser` used to create all four rc files; a new `~/.bash_profile` makes bash login shells stop reading `~/.profile`. *Reproduced before the fix.* | B3 | **Mitigated — F-03.** Only existing files are edited; if none exists a single file for `$SHELL` is created. The one exception is `.zshrc` (in `$ZDOTDIR` when set) for zsh users, since zsh reads none of the others and a new `.zshrc` shadows nothing. `Unset` deletes an rc file that held nothing but our block. |
| T5 | **False success.** Every backend call used to be `_ = run…(…)`; the API returned `nil` even if every command failed (GPO-blocked `reg.exe`, missing admin rights, wrong service). | B2 | **Mitigated — F-09.** Every failing step is collected and returned (`ErrToolMissing` when nothing could be applied); `IsNonCritical`/`RequiresElevation` are kept for optional steps. Real-registry test with a denied write. |
| T6 | **Incomplete restore.** `WithProxy*` used to restore via `SetMulti`, which left the temporary HTTPS/SOCKS proxy in place, and `ScopeShell` restore wiped pre-existing env vars. | `sysproxy.go` | **Mitigated — F-10, F-11.** Backends snapshot every setting and restore it exactly (also fields that were empty); the environment is snapshotted per variable. Tested against real `networksetup`, dconf and the Windows registry. |
| T7 | Wrong protocol configured: the scheme of the URL used to be ignored (a `socks5://` URL became an HTTP proxy). | all backends | **Mitigated — F-13.** The scheme decides the field; fields the scheme does not use are cleared. |
| T8 | Kill/crash between `Set` and restore in `WithProxy` leaves the temporary proxy installed permanently (`defer` does not run on `SIGKILL`/power loss). | `WithProxy` | **Accepted** — document; consider a recovery file. |

### R — Repudiation

| # | Threat | Where | Status |
|---|--------|-------|--------|
| R1 | Changing the system proxy is a security-relevant action, but logging is off by default (`SetLogger(nil)`), and log lines carry no actor, host, or timestamp unless the sink adds them. | `logger.go` | **Partial** — acceptable for a library if documented; recommend a structured `Logger` extension and a documented recommended sink. |
| R2 | `ScopeShell` and failure paths (`SetMulti`, `SetPAC`) log nothing at all, so partial application leaves no trail. | `sysproxy.go` | **Mitigated — F-18.** `SetMulti` and `SetPAC` log like `Set` (credentials redacted). |

### I — Information disclosure

| # | Threat | Where | Status |
|---|--------|-------|--------|
| I1 | **Credentials in logs and errors.** `logf` lines and validation/parse errors printed the full URL including the password (`url.Error` repeats its input). *Reproduced before the fix.* | B5 | **Mitigated — F-02.** `redactURL` on every log line and error; tests assert the password never appears. |
| I2 | **Credentials on command lines.** `networksetup -setwebproxy … user pass`, `kwriteconfig6/5 … rawURL`, `git config http.proxy <url>`, `npm config set proxy <url>` are visible to every local user through the process list for the lifetime of the child. | B2 | **Accepted (documented in README)** — these tools have no stdin mode; use `ScopeShell` where possible. |
| I3 | **Credentials in world-readable `/etc/environment`** (`0644`). *Reproduced before the fix.* | B3 | **Mitigated — F-02.** Userinfo and fragment are stripped; `Set` returns a non-critical error saying credentials were omitted. |
| I4 | Credentials persisted in plain text in rc files (0600), `.curlrc`, `.wgetrc`, `pip.conf`, and by `git`/`npm` in their own files (permissions chosen by those tools / umask). | B3 | **Accepted** with documentation; `ScopeShell` is the safe default for secrets. |
| I5 | The CLI echoed the full proxy URL (with password) in `--json` output, and takes it as a positional argument (shell history, process list). | `cmd/sysproxy` | **Mitigated — F-02, I5.** Output of `set`, `pac`, `check`, `get` and `get --json` is redacted (`user:xxxxx@host`, PAC URLs included); `sysproxy set -` (also `pac`, `check`) reads the URL from stdin. The password still reaches `networksetup`/`git`/`npm` on their command lines (I2). |
| I6 | `Set` copies the proxy URL into the *current process's* environment even for `ScopeGlobal`/`ScopeUser`, so every child process the host application starts inherits the credentials. | `env.go` | **Accepted** — documented in the README (Security); making it opt-in would change the API. |

### D — Denial of service

| # | Threat | Where | Status |
|---|--------|-------|--------|
| D1 | A malformed `/etc/environment` (T3) or an rc file with a broken quote (T1) breaks logins/shells. | B3 | **Mitigated — F-01, F-05** (quoting, atomic writes, trailing newline) |
| D2 | Wrong proxy applied (T5/T7/F-14: empty port, IPv6 without brackets) cuts all connectivity until fixed by hand. | backends | **Mitigated — F-09, F-13, F-14** (errors reported, scheme-aware fields, default ports, bracketed IPv6) |
| D3 | `bufio.Scanner`'s default 64 KiB token limit made `readLines` fail on a long line in `.curlrc`/`pip.conf`, so `Set`/`Unset` for that app errored for good. | `appconfig.go` | **Mitigated.** Lines up to 16 MiB are read (`TestReadLines_LongLine`). |
| D4 | `Check` opens a TCP connection to any host:port supplied by the caller; honours the context deadline. | `check.go` | **Mitigated** (timeout). See E3 for the SSRF angle. |
| D5 | External commands are bound to the caller's context; CLI uses 10 s timeouts. | all | **Mitigated** |

### E — Elevation of privilege

| # | Threat | Where | Status |
|---|--------|-------|--------|
| E1 | T1 turned into privilege escalation: code injected into a shared or root-owned start-up file (e.g. when the tool runs under `sudo` with `HOME` preserved) runs with the victim's rights. | B3 | **Mitigated — F-01** (see T1). Under `sudo` the owner of an existing file is preserved and a new file takes the owner of its directory, so a user's rc file does not become root-owned. |
| E2 | S1 (PATH hijack) in a privileged invocation runs attacker code as root. | B2 | **Mitigated — S1** |
| E3 | **`Check` as an SSRF / port-scan primitive** when a service exposes it to untrusted input (it reveals open/closed state of internal host:port pairs). | `check.go` | **Accepted** — it is the function's purpose; document that untrusted input must be allow-listed by the caller. |
| E4 | The `//nolint:gosec` exception for launching a subprocess sits in `systemCommand` (and on `git`/`npm`), so gosec no longer guards the argument construction. Argument values are fixed strings plus validated data today; keep it that way. | all backends | **Accepted**, review on each change. |

## 6. Residual risk summary

| Risk | Rating | Needed before 1.0? |
|------|--------|--------------------|
| T1/E1 code injection into start-up files | ~~High~~ fixed on `dev` | — (PowerShell quoting verified on a real Windows runner). |
| T5 false success (errors swallowed) | ~~High~~ fixed on `dev` | — |
| T4/T2 data loss in rc files and `/etc/environment` | ~~High/Medium~~ fixed on `dev` | — |
| T6 incomplete restore in `WithProxy*` (F-10, F-11) | ~~High/Medium~~ fixed on `dev` | — |
| I1, I3, I5 credential exposure (logs, `/etc/environment`, CLI output, CLI argument) | ~~Medium~~ fixed on `dev` | — |
| I2 credentials on child-process command lines | **Medium** | Accepted, documented. |
| S1/S2 PATH hijack, `sudo` confusion | ~~Medium~~ fixed on `dev` | — |
| S4 plain-`http://` PAC, R1 audit trail, T8 crash recovery | **Low** | Documentation only. |

## 7. Recommended mitigations (ordered)

Items 1–8 below are implemented (regression tests, two quoting fuzz targets, CI on real systems).

1. **Quote or reject.** Add one `shellQuote` (POSIX `'\''`) and one `psQuote` (`''`) helper and use them for every value written to a start-up file; validate `NoProxy` against ASCII host-name characters (`[A-Za-z0-9._*:/,;<>[]% -]`; internationalised names in punycode); parse PAC URLs with `url.Parse` and reject control characters.
2. **Own your lines.** Write rc/PS/`/etc/environment` changes inside a marker block (`# >>> go-sysproxy >>>` … `# <<< go-sysproxy <<<`), replace the block idempotently, and remove only that block. Only touch rc files that already exist.
3. **Atomic writes.** Temp file in the same directory → `fsync` → `rename`, preserving mode; serialise with a process-wide mutex (and `flock` where practical).
4. **Report errors.** Collect exit status of every backend command; return an aggregated error, `ErrToolMissing` when nothing could be applied, and keep `IsNonCritical`/`RequiresElevation` for genuinely optional steps.
5. **Redact.** Log and print `u.Redacted()`; never write credentials to `/etc/environment`.
6. **Exact restore.** Give backends an "apply exactly this config" operation so `WithProxy*` restores empty fields and `NoProxy` too; snapshot/restore env vars for `ScopeShell`.
7. **System tool paths** for system binaries (not `PATH` for root); warn when running as root with `SUDO_USER` set.
8. Add regression tests for each *reproduced* item above, and seed the fuzz corpus with the injection strings.
