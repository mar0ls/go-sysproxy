# go-sysproxy — threat model

## 1. Document control

| Item | Value |
|------|-------|
| Subject | `github.com/mar0ls/go-sysproxy` — the Go library and the `cmd/sysproxy` command-line tool |
| Reviewed against | `main` at commit `9b9fdce` (release candidate v0.6.0) |
| Date | 2026-10-31 |
| Method | STRIDE per trust boundary, qualitative risk rating (section 5.1) |
| Status | Living document. `TestThreatModel_MatchesCode` fails the build when this file and the code drift apart (section 9) |
| Related | `README.md` (user-facing behaviour and security notes) |

## 2. Scope and security objectives

**In scope**

- The exported Go API (`Set`, `SetMulti`, `SetPAC`, `Unset`, `Get`, `GetConfig`, `WithProxy`, `WithProxyMulti`, `WriteAppConfig`, `ClearAppConfig`, `Check`) and everything it writes: the operating system proxy store, shell start-up files, `/etc/environment`, tool configuration files, the process environment, and (Windows) Credential Manager.
- The `sysproxy` CLI, including its argument handling, output and exit codes.
- The smoke scripts under `scripts/smoke/` and the CI workflows insofar as they exercise the above.

**Out of scope**

- The security of the proxy or PAC server the user chooses, and of the traffic sent through it.
- Defects of the operating system's own stores and tools (`networksetup`, `gsettings`, `reg.exe`, WinINET, dconf, `kioslaverc` handling).
- Confidentiality against malware that already runs as the same user (it can read and write the same files); the library aims only not to make its job easier.
- The Go toolchain and the CI platform (assumed trusted, see section 4.3).

**Security objectives**

| ID | Objective |
|----|-----------|
| O1 | Data supplied to the API or the CLI never becomes code: no command or script injection into shells, PowerShell, or other tools. |
| O2 | The proxy configuration is changed exactly as requested and no further, and `WithProxy` restores exactly what it changed. |
| O3 | Proxy credentials are not disclosed through logs, errors, output, or world-readable files, and are stored only where the user asked for them to be. |
| O4 | The library reports the truth: a change that did not happen is an error, not a success. |
| O5 | The tool does not make the machine unusable (broken shell start-up, missing connectivity) and does not damage files it does not own. |
| O6 | Privileged invocations do not run attacker-chosen code or write on behalf of the wrong account without telling the caller. |

## 3. System overview

### 3.1 Components

| Component | Source | Role |
|-----------|--------|------|
| Public API | `sysproxy.go`, `backend.go`, `context_helpers.go` | Entry points, scopes (`ScopeShell`, `ScopeUser`, `ScopeGlobal`), snapshot/restore for `WithProxy` |
| Validation | `validate.go` | Proxy URL, PAC URL and `NoProxy` checks, credential redaction |
| Backends | `sysproxy_darwin.go`, `sysproxy_linux.go`, `kde_linux.go`, `sysproxy_windows.go`, `windows_parse.go` | Talk to `networksetup`, `gsettings`, `kwriteconfig`/`kreadconfig`, and the registry through `reg.exe` |
| Windows native calls | `winapi_windows.go` | WinINET notification, shell folders, Credential Manager API |
| Tool execution | `sysexec.go`, `sysexec_unix.go`, `sysexec_windows.go` | Resolution of system tools and construction of the subprocess |
| File editing | `managedblock.go`, `rcfile_unix.go`, `etcenv.go`, `fsutil.go`, `fsutil_unix.go` | Marked blocks in start-up files, `/etc/environment`, atomic writes |
| Application configs | `appconfig.go` | `.curlrc`, `.wgetrc`, `pip.conf`, `git config`, `npm config` |
| Process environment | `env.go` | `http_proxy` and related variables of the running process |
| Privilege awareness | `sudo.go` | Detection of root running through `sudo` |
| Reachability check | `check.go` | TCP connection to a proxy endpoint |
| CLI | `cmd/sysproxy/main.go` | Argument parsing, output, exit codes |

### 3.2 Privilege model

The library never escalates. It runs with the rights of its caller and reports, with `RequiresElevation`, when a step needed more (for example `/etc/environment` without root). Per platform: macOS changes the proxy of every network service through `networksetup`; Linux writes GNOME (dconf) and KDE (`kioslaverc`) settings of the current user and, with root, `/etc/environment`; Windows writes the current user's `HKCU` Internet Settings and Credential Manager.

### 3.3 Data flows and trust boundaries

```
 caller input ──────────┐
 (URL, PAC, NoProxy,    │   [B1] validation
  CLI argv / stdin)     ▼
                 ┌───────────────────┐   [B2] subprocess, no shell
                 │ go-sysproxy core  │──────────────► networksetup, route, gsettings,
                 │ parse + backends  │                kwriteconfig6|5, kreadconfig6|5, reg,
                 └─┬───────┬──────┬──┘                Credential Manager API, git, npm
                   │       │      │                              │
        [B3] files │       │      │ [B5] logger sink             ▼
                   │       │      ▼                      OS proxy stores
                   ▼       ▼   caller-supplied Logger    (SystemConfiguration, dconf,
   rc files, /etc/environment,                            kioslaverc, WinINET registry)
   tool configs, PowerShell profile   [B4] process environment
```

| Boundary | Crossing | Why it matters |
|----------|----------|----------------|
| B1 | Untrusted string becomes a parsed value | Origin of every injection risk |
| B2 | The process starts an external binary | The binary's identity, its arguments (credentials), its output |
| B3 | The process writes files shared with other programs and users | Persistence, code execution on next login, ownership, atomicity |
| B4 | The process changes its own environment | Inherited by every child process |
| B5 | The process hands text to an arbitrary logging sink | Credential disclosure |

## 4. Assets, actors and assumptions

### 4.1 Assets

| ID | Asset | Why it matters |
|----|-------|----------------|
| A1 | Integrity of the OS proxy configuration | Controls where the machine's traffic goes (interception, exfiltration, loss of connectivity) |
| A2 | Proxy credentials (`user:password@`) | Reusable secrets, often corporate SSO or paid proxy accounts |
| A3 | Shell start-up files and the PowerShell profile | Run at every shell start: write access is persistent code execution |
| A4 | `/etc/environment` | System-wide, read at login by every user, world-readable |
| A5 | Tool configs (`.curlrc`, `.wgetrc`, `pip.conf`, git and npm configuration) | Hold proxy settings and credentials outside the OS store |
| A6 | Windows Credential Manager entries | Keyed by host name; may belong to unrelated services |
| A7 | Availability of the host | A bad start-up file or a stuck proxy cuts the user off |

### 4.2 Actors

| Actor | Trust | Notes |
|-------|-------|-------|
| Library consumer | Trusted, inputs not | Proxy URL, PAC URL and `NoProxy` often come from configuration files or a remote API |
| CLI user | Trusted for their own account | Arguments are visible to other local users in the process list |
| Other local user, unprivileged process | Untrusted | Can read world-readable files, watch the process list, write to directories earlier in `PATH` |
| Same-user malware | Out of scope for A2 and A3 confidentiality | Can already write the same files |
| Network attacker | Untrusted | Can alter a PAC fetched over plain `http://`, sniff unencrypted proxy credentials |
| Proxy or PAC operator | Untrusted for content | The tool deliberately sends traffic through it |

### 4.3 Assumptions

| ID | Assumption | If it fails |
|----|------------|-------------|
| AS1 | The OS tools and stores behave as documented and are not malicious | Out of scope |
| AS2 | The system tool directories (`/usr/bin`, `/bin`, `/usr/sbin`, `/sbin`, the Windows system directory) are writable only by administrators | An attacker with that access already owns the machine |
| AS3 | `PATH`, `HOME`, `ZDOTDIR` and `SUDO_USER` are controlled by the caller's environment and may be influenced by an attacker for an unprivileged process | Threats S1, S5, T9 |
| AS4 | The build toolchain and the CI actions are trusted | Supply-chain risk, not addressed here; the module has no third-party dependencies |

## 5. Threat analysis

### 5.1 Rating and status

Risk is rated qualitatively from impact and likelihood: **Critical** (code execution as another user, or full credential disclosure, easy to trigger), **High** (code execution or traffic redirection needing a precondition), **Medium** (corruption of user configuration, misleading success, temporary outage), **Low** (diagnostic or hard to exploit). *Inherent* is the rating without the controls, *Residual* with them.

Status values: **Mitigated** (controls and tests exist, residual risk at most Medium), **Partial** (a control exists but a gap remains, listed in section 6), **Accepted** (conscious residual risk, listed in section 6), **Open** (no control yet, listed in section 6).

### 5.2 Controls

| ID | Control | Implementation | Evidence |
|----|---------|----------------|----------|
| C1 | Input validation: proxy URL needs scheme, host and port 1–65535; PAC URL only http, https or file; no whitespace or control characters; `NoProxy` limited to ASCII host-name characters | `validate.go` | `TestValidateNoProxy`, `TestPublicAPI_RejectsInjectionAttempts`, `FuzzParse`, `FuzzValidateProxyURL`, `FuzzValidatePACURL` |
| C2 | Every value written to a shell or PowerShell start-up file is quoted | `managedblock.go`, `rcfile_unix.go` | `TestSetUser_QuotesHostileValues`, `FuzzShellQuote`, `FuzzPSQuote`, `TestIntegrationWindows_UserScopeProfile` |
| C3 | Edits happen only inside a marked block, only in files that already exist (plus `.zshrc` for zsh); `Unset` removes exactly that block | `managedblock.go`, `rcfile_unix.go` | `TestUnsetUser_KeepsUnrelatedLines`, `TestSetUser_Idempotent`, `TestSetUser_DoesNotCreateShadowingFiles`, `TestSetUser_CreatesOnlyOneFileWhenNoneExist`, `TestSetUser_ZshUserGetsAZshrcEvenWithOtherRCFiles`, `TestSetUser_ZshHonoursZDOTDIR` |
| C4 | Atomic file replacement that preserves mode, owner and symlinks, robust against very long lines | `fsutil.go`, `fsutil_unix.go`, `appconfig.go` | `TestWriteFileAtomic_LeavesNoTempFiles`, `TestWriteFileAtomic_PreservesExistingMode`, `TestWriteFileAtomic_FollowsSymlink`, `TestReadLines_LongLine` |
| C5 | Credentials are redacted in logs, errors and CLI output | `validate.go`, `cmd/sysproxy/main.go`, `sysproxy.go` | `TestLogger_DoesNotLeakPassword`, `TestLogger_CoversSetMultiAndSetPACWithoutPasswords`, `TestValidationErrorsDoNotLeakPassword`, `TestSetJSON_RedactsPassword`, `TestGet_NeverPrintsPasswords`, `TestPAC_NeverPrintsPasswords` |
| C6 | `/etc/environment` is edited in a marked block and never receives credentials | `etcenv.go` | `TestWriteEtcEnvironment_DropsCredentials`, `TestWriteEtcEnvironment_KeepsAdminNoProxyAndFormat`, `TestClearEtcEnvironment_RestoresOriginalExactly`, `TestClearEtcEnvironment_UntouchedFileWithoutTrailingNewline` |
| C7 | Failures of backend steps are collected and returned; optional steps are classified non-critical; a setting that was not stored is detected by reading it back | `backend_util.go`, `sysproxy_linux.go`, `sysproxy_darwin.go`, `sysproxy_windows.go`, `errors.go` | `TestIntegrationWindows_RegistryWriteDeniedIsReported`, `TestIntegrationLinux_NoSilentFailureWithoutSessionBus`, `TestLinuxSetGlobal_ReportsDesktopFailure`, `TestNetworksetupFailuresPropagate`, `TestReadRegValue_AbsentVersusUnreadable` |
| C8 | `WithProxy` snapshots every setting it can change, refuses to start when it cannot, and restores exactly | `sysproxy.go`, `sysproxy_darwin.go`, `sysproxy_linux.go`, `sysproxy_windows.go` | `TestWithProxy_SnapshotFailureChangesNothing`, `TestWithProxy_RestoreErrorIsReported`, `TestWithProxy_NonCriticalRestoreErrorIsStillAFailureAndNeverTaints`, `TestIntegrationWindows_WithProxyRestoresExactly`, `TestIntegrationLinux_WithProxyRestoresExactly`, `TestIntegrationDarwin_WithProxyRestoresExactly` |
| C9 | System tools are resolved from the system directories; only an unprivileged process falls back to `PATH` | `sysexec.go`, `sysexec_unix.go`, `sysexec_windows.go` | `TestLookSystemTool_SystemDirectoryBeatsPATH`, `TestLookSystemTool_PATHFallbackOnlyWithoutPrivileges`, `TestSystemCommand_RunsTheSystemToolNotTheOneInPATH`, `TestLookSystemTool_RegComesFromTheSystemDirectory` |
| C10 | Root running through `sudo` is detected and reported; a file root creates takes the owner of its directory; `SUDO_USER` only words a warning | `sudo.go`, `fsutil.go`, `sysproxy_linux.go` | `TestSudoCaller`, `TestSudoHomeWarning`, `TestLinuxGlobal_WarnsWhenRunAsRootThroughSudo`, `TestLinuxGlobal_NoSudoWarningWithoutRoot`, `TestWriteFileAtomic_NewFileTakesTheOwnerOfItsDirectoryWhenRoot` |
| C11 | Windows credentials go through the Credential Manager API, marked as ours; entries that are not ours are neither overwritten nor deleted | `winapi_windows.go`, `sysproxy_windows.go` | `TestIntegrationWindows_ProxyCredentials` |
| C12 | The CLI can read a URL from standard input so that a password stays out of the process list and the shell history | `cmd/sysproxy/main.go` | `TestURLArg_FromStdin`, `TestURLArg_StdinPasswordIsMaskedInOutput` |
| C13 | The URL scheme decides which setting is written, unused settings are cleared, ports default per scheme, IPv6 is bracketed, and settings the caller did not name are kept | `validate.go`, `helpers.go`, `sysproxy_darwin.go`, `sysproxy_linux.go`, `sysproxy_windows.go`, `windows_parse.go` | `TestSetGlobal_SchemeDecidesTheField`, `TestSetGlobal_DefaultPortAndIPv6`, `TestIntegrationLinux_SchemeDefaultPortAndMulti`, `TestGetGlobalConfig_IPv6HostsAreBracketed`, `TestIntegrationWindows_SetMultiKeepsWhatItIsNotGiven` |
| C14 | Subprocesses and the reachability check are bound to the caller's context; the CLI sets timeouts | `sysexec.go`, `check.go`, `cmd/sysproxy/main.go` | `TestSystemCommand_HonoursContextCancellation`, `TestCheckDeadlineExceededUsesDefaultPort` |

### 5.3 Threat register

#### Spoofing

| ID | Threat | Component | Inherent | Controls | Evidence | Residual | Status |
|----|--------|-----------|----------|----------|----------|----------|--------|
| S1 | A binary planted earlier in `PATH` is run instead of a system tool, with the caller's privileges and with proxy credentials on its command line; worst when the CLI runs as root | `sysexec.go` | High | C9 | `TestSystemCommand_RunsTheSystemToolNotTheOneInPATH`, `TestLookSystemTool_PATHFallbackOnlyWithoutPrivileges` | Low | Mitigated |
| S2 | Root running through `sudo` changes root's desktop settings and files while the caller believes they changed the invoking user's | `sudo.go`, `sysproxy_linux.go` | Medium | C10 | `TestLinuxGlobal_WarnsWhenRunAsRootThroughSudo`, `TestSudoHomeWarning` | Low | Mitigated |
| S3 | A Credential Manager entry keyed by host name is overwritten or deleted although it belongs to another service | `winapi_windows.go` | Medium | C11 | `TestIntegrationWindows_ProxyCredentials` | Low | Mitigated |
| S4 | A PAC file fetched over plain `http://` is altered on the path and then controls the routing of the whole machine | `validate.go` | Medium | C1 | `FuzzValidatePACURL` | Medium | Accepted |
| S5 | A forged `SUDO_USER` misleads the sudo detection | `sudo.go` | Low | C10 | `TestSudoCaller` | Low | Mitigated |

#### Tampering

| ID | Threat | Component | Inherent | Controls | Evidence | Residual | Status |
|----|--------|-----------|----------|----------|----------|----------|--------|
| T1 | Proxy URL, PAC URL or `NoProxy` is spliced into a start-up file and runs as code at the next shell start | `managedblock.go`, `rcfile_unix.go`, `sysproxy_windows.go` | Critical | C1, C2 | `TestSetUser_QuotesHostileValues`, `TestPublicAPI_RejectsInjectionAttempts`, `FuzzShellQuote`, `FuzzPSQuote`, `TestIntegrationWindows_UserScopeProfile` | Low | Mitigated |
| T2 | Lines the tool does not own are deleted or rewritten (user aliases, a custom `NO_PROXY`, an administrator's `/etc/environment`) | `managedblock.go`, `etcenv.go` | High | C3, C6 | `TestUnsetUser_KeepsUnrelatedLines`, `TestWriteEtcEnvironment_KeepsAdminNoProxyAndFormat`, `TestClearEtcEnvironment_RestoresOriginalExactly` | Low | Mitigated |
| T3 | Two writers interleave or a crash truncates a configuration file | `fsutil.go` | Medium | C4 | `TestWriteFileAtomic_LeavesNoTempFiles`, `TestWriteFileAtomic_PreservesExistingMode` | Low | Partial |
| T4 | A newly created file changes shell behaviour (a new `.bash_profile` hides `.profile`) | `rcfile_unix.go` | Medium | C3 | `TestSetUser_DoesNotCreateShadowingFiles`, `TestSetUser_CreatesOnlyOneFileWhenNoneExist` | Low | Mitigated |
| T5 | A failed command is reported as success and the caller proceeds as if traffic were proxied | `backend_util.go` | High | C7 | `TestIntegrationWindows_RegistryWriteDeniedIsReported`, `TestIntegrationLinux_NoSilentFailureWithoutSessionBus`, `TestNetworksetupFailuresPropagate` | Low | Mitigated |
| T6 | `WithProxy` leaves part of the temporary configuration behind or loses pre-existing settings and environment variables | `sysproxy.go` | High | C8 | `TestWithProxy_SnapshotFailureChangesNothing`, `TestIntegrationWindows_WithProxyRestoresExactly`, `TestIntegrationLinux_WithProxyRestoresExactly`, `TestIntegrationDarwin_WithProxyRestoresExactly` | Low | Mitigated |
| T7 | The URL scheme is ignored and the wrong setting is configured (a SOCKS proxy used as an HTTP proxy), or a stale setting survives | all backends | Medium | C13 | `TestSetGlobal_SchemeDecidesTheField`, `TestIntegrationLinux_SchemeDefaultPortAndMulti` | Low | Mitigated |
| T8 | The process is killed between `Set` and the restore of `WithProxy`, so the temporary proxy stays | `sysproxy.go` | Medium | C8 | `TestWithProxy_RestoresWhenFnPanics` | Medium | Accepted |
| T9 | A root invocation that uses another user's home directory follows a symbolic link planted in a start-up file and edits a file elsewhere | `fsutil.go` | Medium | C4, C10 | `TestWriteFileAtomic_FollowsSymlink` | Medium | Accepted |
| T10 | WinINET, notified after a value was deleted, writes its cached settings back and resurrects the temporary proxy | `sysproxy_windows.go`, `winapi_windows.go` | Medium | C8 | `TestIntegrationWindows_WithProxyRestoresExactly` | Low | Mitigated |
| T11 | A registry read that failed (access denied) is taken for an absent value, so a snapshot or a merge silently discards settings | `sysproxy_windows.go` | Medium | C7 | `TestReadRegValue_AbsentVersusUnreadable` | Low | Mitigated |
| T12 | A call changes settings the caller did not name (a bypass list alone enables the proxy, one kind replaces the others) | `sysproxy_windows.go`, `windows_parse.go` | Medium | C13 | `TestIntegrationWindows_SetMultiKeepsWhatItIsNotGiven` | Low | Mitigated |

#### Repudiation

| ID | Threat | Component | Inherent | Controls | Evidence | Residual | Status |
|----|--------|-----------|----------|----------|----------|----------|--------|
| R1 | Changing the system proxy is security relevant, but logging is off by default and lines carry no actor or timestamp unless the sink adds them | `logger.go` | Low | C5 | `TestLogger_DoesNotLeakPassword` | Low | Partial |
| R2 | `SetMulti` and `SetPAC` leave no trail, so a partial application cannot be reconstructed | `sysproxy.go` | Low | C5 | `TestLogger_CoversSetMultiAndSetPACWithoutPasswords` | Low | Mitigated |

#### Information disclosure

| ID | Threat | Component | Inherent | Controls | Evidence | Residual | Status |
|----|--------|-----------|----------|----------|----------|----------|--------|
| I1 | The password appears in log lines, error messages or validation errors | `validate.go`, `sysproxy.go` | High | C5 | `TestLogger_DoesNotLeakPassword`, `TestValidationErrorsDoNotLeakPassword`, `TestSetGlobal_ReportsFailureWithoutLeakingPassword` | Low | Mitigated |
| I2 | Credentials are visible to local users in the command line of `networksetup`, `kwriteconfig`, `git` or `npm` while the child runs | `sysproxy_darwin.go`, `sysproxy_linux.go`, `appconfig.go` | Medium | C5 | `TestLogger_DoesNotLeakPassword` | Medium | Accepted |
| I3 | Credentials are written to the world-readable `/etc/environment` | `etcenv.go` | High | C6 | `TestWriteEtcEnvironment_DropsCredentials` | Low | Mitigated |
| I4 | Credentials persist in plain text in rc files (mode 0600), `.curlrc`, `.wgetrc`, `pip.conf`, and in the files git and npm keep | `rcfile_unix.go`, `appconfig.go` | Medium | C3 | `TestSetUser_Idempotent` | Medium | Accepted |
| I5 | The CLI prints the full URL with password, or the password sits in the argument list and shell history | `cmd/sysproxy/main.go` | Medium | C5, C12 | `TestGet_NeverPrintsPasswords`, `TestPAC_NeverPrintsPasswords`, `TestURLArg_FromStdin`, `TestURLArg_StdinPasswordIsMaskedInOutput` | Low | Mitigated |
| I6 | `Set` copies the proxy URL, password included, into the process's own environment, so every child process inherits it | `env.go` | Medium | C5 | `TestUnsetEnvVars_ClearsEveryManagedVariable` | Medium | Accepted |
| I7 | KDE keeps the proxy URL, login included, in the user's `kioslaverc`, and a stale copy would survive `Unset` | `sysproxy_linux.go` | Medium | C7 | `TestLinuxKDE_UnsetLeavesNoCredentialsBehind` | Low | Partial |

#### Denial of service

| ID | Threat | Component | Inherent | Controls | Evidence | Residual | Status |
|----|--------|-----------|----------|----------|----------|----------|--------|
| D1 | A malformed `/etc/environment` or a start-up file with a broken quote breaks logins or shells | `etcenv.go`, `managedblock.go` | High | C2, C4, C6 | `TestSetUser_QuotesHostileValues`, `TestWriteFileAtomic_LeavesNoTempFiles` | Low | Mitigated |
| D2 | A wrong proxy (empty port, unbracketed IPv6, wrong field) cuts connectivity until fixed by hand | all backends | Medium | C1, C13 | `TestSetGlobal_DefaultPortAndIPv6`, `TestGetGlobalConfig_IPv6HostsAreBracketed` | Low | Mitigated |
| D3 | A configuration line longer than the scanner limit makes `Set` and `Clear` fail for good | `appconfig.go` | Low | C4 | `TestReadLines_LongLine` | Low | Mitigated |
| D4 | `Check` blocks on an unreachable endpoint | `check.go` | Low | C14 | `TestCheckDeadlineExceededUsesDefaultPort` | Low | Mitigated |
| D5 | A tool that hangs blocks the caller | `sysexec.go` | Medium | C14 | `TestSystemCommand_HonoursContextCancellation` | Low | Mitigated |

#### Elevation of privilege

| ID | Threat | Component | Inherent | Controls | Evidence | Residual | Status |
|----|--------|-----------|----------|----------|----------|----------|--------|
| E1 | Injection into a shared or root-owned start-up file runs with the victim's rights, or a root-created file becomes root-owned in a user's home | `rcfile_unix.go`, `fsutil.go` | High | C2, C10 | `TestSetUser_QuotesHostileValues`, `TestWriteFileAtomic_NewFileTakesTheOwnerOfItsDirectoryWhenRoot` | Low | Mitigated |
| E2 | A hijacked tool (S1) runs as root | `sysexec.go` | High | C9 | `TestLookSystemTool_SystemDirectoryBeatsPATH` | Low | Mitigated |
| E3 | `Check` is used as an SSRF or port-scan primitive when a service passes it untrusted input | `check.go` | Medium | C14 | `TestCheckInvalidURL` | Medium | Accepted |
| E4 | The linter exception for starting a subprocess means gosec no longer guards how arguments are built | `sysexec.go` | Low | C9 | `TestSystemCommand_RunsTheSystemToolNotTheOneInPATH` | Low | Accepted |

## 6. Residual risks

| ID | Residual risk | Rationale | Next step |
|----|---------------|-----------|-----------|
| S4 | A plain `http://` PAC can be spoofed on the path | The scheme is allowed because corporate PAC files commonly use it | Document the risk next to `SetPAC`; consider an option that accepts only `https` |
| T3 | No cross-process lock: two processes editing the same file can lose an update | Writes are atomic and serialised inside one process; cross-process locking is not portable | Add `flock` on Unix if a user reports it |
| T8 | A killed process leaves the temporary proxy in place | `defer` does not run on `SIGKILL` or power loss | Document; consider a recovery file |
| T9 | Root using another user's home follows a symlink planted there | Following symlinks is intended (dotfile managers such as stow); the exposure needs root acting on a home it does not own | Refuse to write through a symlink owned by another user when privileged |
| R1 | No actor or timestamp in log lines | The logger interface takes a string; the sink can add the rest | Document a recommended sink |
| I2 | Credentials in the command line of OS tools | `networksetup`, `kwriteconfig`, `git` and `npm` have no stdin mode | Prefer `ScopeShell` for secrets; documented |
| I4 | Credentials stored in plain text in start-up files and tool configs | The user asked for persistence; files are mode 0600 where this tool creates them | Documented |
| I6 | The process environment holds the credentials | Changing it would change the API | Documented |
| I7 | KDE stores the login in the URL in `kioslaverc` | KDE has no other way to hold proxy authentication; `Unset` empties the keys | Documented |
| E3 | `Check` reveals whether internal endpoints are open | That is its purpose | Callers must allow-list untrusted input |
| E4 | gosec does not inspect how subprocess arguments are built | The exception is confined to `sysexec.go` and `appconfig.go` | Review on every change to those files |

## 7. Verification evidence

| Activity | Where | What it covers |
|----------|-------|----------------|
| Unit tests on macOS, Ubuntu and Windows, Go 1.23 and stable | `.github/workflows/test.yml` | All controls with fakes of the OS tools |
| Static analysis: golangci-lint with gosec, staticcheck, errcheck, nolintlint | `.github/workflows/test.yml` | C1–C14 source quality; lint also run locally for `GOOS` darwin, linux and windows |
| Integration tests against the real proxy stores | `.github/workflows/integration.yml` | Windows registry, PowerShell, Credential Manager and WinINET; Linux dconf with KDE 5 tools; macOS `networksetup` |
| CLI smoke scripts with assertions | `scripts/smoke/lib.sh`, `scripts/smoke/linux.sh`, `scripts/smoke/macos.sh`, `scripts/smoke/windows.ps1` | Exit codes, `get --json`, native settings after each step |
| Fuzzing, five targets | `.github/workflows/fuzz.yml` | `FuzzParse`, `FuzzValidateProxyURL`, `FuzzValidatePACURL`, `FuzzShellQuote`, `FuzzPSQuote` (nightly) |
| Manual run on a Debian 13 VM, 2026-10-31 | not automated | KDE Plasma 6 and 5 tools and GNOME schemas, as user and as root |
| Manual run as root in a Debian 13 container, 2026-10-31 | not automated | System tool resolution with a hostile `PATH`, `SUDO_USER` warning |
| `govulncheck`, 2026-10-31 | not automated | One finding, in the standard library of the local Go 1.26.2 toolchain; none in this module |

## 8. Limitations of this analysis

- It is a design and code review with tests, not a penetration test or a formal verification.
- Full desktop sessions (GNOME Shell, Plasma) were not exercised; the Linux runs use a session bus with dconf and the KDE tools.
- The Windows behaviour for a registry key that cannot be read is tested with a faked `reg query`, not on a real read-denied key.
- Tests that need root (file ownership) run in a container, not in CI.
- The threats are those found by the reviewers; the list is not proof that no other threat exists.

## 9. Maintenance

Update this document, in the same change, when a change adds or removes a backend, a file the tool writes, a subprocess, an input source, a control, or a test cited here. `TestThreatModel_MatchesCode` (`threatmodel_test.go`) runs in the `Go` workflow and fails when:

- a threat or control row is malformed, or an identifier is used twice;
- a status, risk rating, control, source file, workflow, script or test named here does not exist;
- a **Mitigated** threat names no control or no test, or has a residual risk above Medium, or a residual risk above its inherent risk;
- a threat that is not Mitigated is missing from section 6, or section 6 lists a threat that is Mitigated;
- a control is not used by any threat.

The check cannot judge whether a mitigation is adequate; that remains a review decision.

| Date | Change |
|------|--------|
| 2026-10-31 | Rewritten against v0.6.0: stable identifiers, control catalogue, residual-risk register, CI consistency check |
