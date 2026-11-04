# build-all.ps1 -- release builds of cs-team: 8 variants, static (CGO_ENABLED=0)
#
#   linux amd64 + arm64, darwin amd64 + arm64, windows amd64, freebsd amd64, illumos amd64, solaris amd64
#
# Output (dist\ is not in git):
#   dist\<os>.<arch>\cs-team[.exe]              platform folders
#   dist\release\cs-team-<os>-<arch>[.exe]      GitHub release assets
#   dist\checksums.sha256                       sha256 of every release asset
#
# Usage:  powershell -NoProfile -File build-all.ps1 [-Test]      (Go in PATH; -Test runs "go vet" and "go test ./..." first)
param([switch]$Test)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$variants = @(
    @{ os = 'windows'; arch = 'amd64' },
    @{ os = 'linux';   arch = 'amd64' },
    @{ os = 'linux';   arch = 'arm64' },
    @{ os = 'darwin';  arch = 'amd64' },
    @{ os = 'darwin';  arch = 'arm64' },
    @{ os = 'freebsd'; arch = 'amd64' },
    @{ os = 'illumos'; arch = 'amd64' },
    @{ os = 'solaris'; arch = 'amd64' }
)

if ($Test) {
    Write-Host '== go vet / go test'
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'vet failed' }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'tests failed' }
}

$ver = (Select-String -Path main.go -Pattern 'const version = "([^"]+)"').Matches[0].Groups[1].Value
Write-Host "== cs-team $ver"

if (Test-Path dist) { Remove-Item dist -Recurse -Force }
New-Item -ItemType Directory -Path dist\release | Out-Null

$env:CGO_ENABLED = '0'
$sums = @()
foreach ($v in $variants) {
    $env:GOOS = $v.os; $env:GOARCH = $v.arch
    $ext = ''; if ($v.os -eq 'windows') { $ext = '.exe' }
    $dir = "dist\$($v.os).$($v.arch)"
    New-Item -ItemType Directory -Path $dir | Out-Null
    $bin = "$dir\cs-team$ext"
    go build -trimpath -ldflags '-s -w' -o $bin .
    if ($LASTEXITCODE -ne 0) { throw "build failed: $($v.os)/$($v.arch)" }
    $asset = "dist\release\cs-team-$($v.os)-$($v.arch)$ext"
    Copy-Item $bin $asset
    $h = (Get-FileHash $asset -Algorithm SHA256).Hash.ToLower()
    $sums += "$h  cs-team-$($v.os)-$($v.arch)$ext"
    '{0,-8} {1,-6} {2,10:N0} bytes  {3}' -f $v.os, $v.arch, (Get-Item $asset).Length, $h.Substring(0, 12)
}
# cs-ldapclient: installer for the LDAP client login (cs-ldapclient-<os>[.exe], ldap.cfg next to it)
# windows, linux, freebsd, macOS (intel + arm64) + cs-ldapclient.zip for a USB stick (start scripts copy the program to /tmp)
$env:CGO_ENABLED = '0'; $env:GOARCH = 'amd64'
$lc = @(@('windows', 'cs-ldapclient-windows.exe'), @('linux', 'cs-ldapclient-linux'), @('freebsd', 'cs-ldapclient-freebsd'))
foreach ($l in $lc) {
  $env:GOOS = $l[0]
  $asset = "dist\release\$($l[1])"
  go build -trimpath -ldflags '-s -w' -o $asset ./cmd/cs-ldapclient
  if ($LASTEXITCODE -ne 0) { throw "build failed: cs-ldapclient $($l[0])" }
  $sums += "$((Get-FileHash $asset -Algorithm SHA256).Hash.ToLower())  $($l[1])"
}
# macOS (Open Directory): intel + apple silicon
$env:GOOS = 'darwin'
foreach ($m in @(@('amd64', 'cs-ldapclient-osx'), @('arm64', 'cs-ldapclient-osx-arm64'))) {
  $env:GOARCH = $m[0]
  $asset = "dist\release\$($m[1])"
  go build -trimpath -ldflags '-s -w' -o $asset ./cmd/cs-ldapclient
  if ($LASTEXITCODE -ne 0) { throw "build failed: cs-ldapclient darwin $($m[0])" }
  $sums += "$((Get-FileHash $asset -Algorithm SHA256).Hash.ToLower())  $($m[1])"
}
$env:GOARCH = 'amd64'
$lc += , @('darwin', 'cs-ldapclient-osx')
$lc += , @('darwin', 'cs-ldapclient-osx-arm64')
$stage = 'dist\ldapclient-zip'
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
New-Item -ItemType Directory $stage | Out-Null
foreach ($l in $lc) { Copy-Item "dist\release\$($l[1])" $stage }
foreach ($f in 'start-linux.sh', 'start-freebsd.sh', 'start-osx.command', 'ldap.cfg', 'README.txt') {
  $t = [IO.File]::ReadAllText("$PWD\cmd\cs-ldapclient\pack\$f").Replace("`r`n", "`n")   # LF for the shell scripts
  [IO.File]::WriteAllText("$PWD\$stage\$f", $t, (New-Object Text.UTF8Encoding $false))
}
$zip = 'dist\release\cs-ldapclient.zip'
if (Test-Path $zip) { Remove-Item $zip }
Compress-Archive -Path "$stage\*" -DestinationPath $zip
$sums += "$((Get-FileHash $zip -Algorithm SHA256).Hash.ToLower())  cs-ldapclient.zip"
Remove-Item $stage -Recurse -Force
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
[IO.File]::WriteAllText("$PWD\dist\checksums.sha256", (($sums -join "`n") + "`n"))
Write-Host "== $($variants.Count) variants in dist\release, checksums in dist\checksums.sha256"
