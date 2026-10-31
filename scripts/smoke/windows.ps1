# Windows smoke test. Changes the current user's proxy settings (registry) and
# clears them at the end; refuses to run when a proxy is already configured
# (set $env:FORCE = "1" to override). Exits non-zero on the first failed check.
param(
  [string]$Bin = ".\sysproxy.exe",
  [string]$ProxyUrl = "http://127.0.0.1:8080",
  [string]$PacUrl = "https://example.com/proxy.pac"
)

# 'Continue': Windows PowerShell 5.1 turns native stderr output into a
# terminating error under 'Stop'. Every check below is explicit instead.
$ErrorActionPreference = "Continue"
$Key = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings"

function Fail([string]$Message) {
  Write-Host "FAIL: $Message"
  exit 1
}

function Ok([string]$Message) { Write-Host "ok: $Message" }

# Run <expected exit code> <arguments…>: output goes to $script:Out.
function Run([int]$Want, [string[]]$CliArgs) {
  $script:Out = (& $Bin @CliArgs 2>&1 | Out-String).Trim()
  $got = $LASTEXITCODE
  if ($got -ne $Want) { Fail "'$CliArgs' exited $got, want $Want. Output: $script:Out" }
  Ok "'$CliArgs' -> exit $Want"
}

function Contains([string]$Text) {
  if (-not $script:Out.Contains($Text)) { Fail "output lacks '$Text'. Output: $script:Out" }
  Ok "output contains '$Text'"
}

function Lacks([string]$Text) {
  if ($script:Out.Contains($Text)) { Fail "output unexpectedly contains '$Text'. Output: $script:Out" }
  Ok "output does not contain '$Text'"
}

# Reg <value name>: the registry value, or $null when it does not exist.
function Reg([string]$Name) {
  (Get-ItemProperty -Path $Key -Name $Name -ErrorAction SilentlyContinue).$Name
}

function ExpectReg([string]$Name, $Want) {
  $got = Reg $Name
  if ($got -ne $Want) { Fail "registry $Name = '$got', want '$Want'" }
  Ok "registry $Name = '$Want'"
}

if (-not (Test-Path $Bin)) { Fail "binary not found: $Bin" }

Run 0 @("version")
& $Bin get --json *> $null
if ($LASTEXITCODE -ne 2 -and $env:FORCE -ne "1") {
  Fail "a proxy is already configured (get exited $LASTEXITCODE): this script would erase it. Set `$env:FORCE = '1' to proceed anyway."
}

try {
  Write-Host "--- http proxy fills HTTP only"
  Run 0 @("set", $ProxyUrl, "--scope", "global")
  ExpectReg "ProxyEnable" 1
  ExpectReg "ProxyServer" "127.0.0.1:8080"
  Run 0 @("get")
  if ($script:Out -ne $ProxyUrl) { Fail "get printed '$script:Out', want '$ProxyUrl'" }
  Ok "get printed the proxy URL"
  Run 0 @("get", "--json")
  Contains "`"http`": `"$ProxyUrl`""
  Lacks '"socks"'

  Write-Host "--- socks5 replaces it and fills SOCKS only"
  Run 0 @("set", "socks5://127.0.0.1:1080", "--scope", "global")
  ExpectReg "ProxyServer" "socks=127.0.0.1:1080"
  Run 0 @("get", "--json")
  Contains '"socks": "socks5://127.0.0.1:1080"'
  Lacks '"http"'

  Write-Host "--- credentials never reach the output"
  Run 0 @("set", "http://alice:s3cr3tpw@127.0.0.1:8080", "--scope", "global")
  Lacks "s3cr3tpw"
  Run 0 @("get", "--json")
  Lacks "s3cr3tpw"

  Write-Host "--- PAC"
  Run 0 @("pac", $PacUrl, "--scope", "global")
  ExpectReg "AutoConfigURL" $PacUrl
  ExpectReg "ProxyEnable" 0
  Run 0 @("get", "--json")
  Contains "`"pac`": `"$PacUrl`""

  Write-Host "--- invalid input is refused"
  Run 1 @("set", "not a url", "--scope", "global")
  Run 1 @("pac", "ftp://example.com/p.pac", "--scope", "global")

  Write-Host "--- unset leaves nothing behind"
  Run 0 @("unset", "--scope", "global")
  ExpectReg "ProxyEnable" 0
  ExpectReg "ProxyServer" $null
  ExpectReg "AutoConfigURL" $null
  Run 2 @("get")
  Run 2 @("get", "--json")
}
finally {
  & $Bin unset --scope global *> $null
}

Write-Host "[Windows] done"
