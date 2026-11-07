# 納入する Windows マシン上で実行する。追加インストールや外部通信はしない。
param([string]$OutputDirectory = 'C:\sbom-work')
$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force $OutputDirectory | Out-Null
$os = Get-CimInstance Win32_OperatingSystem
$components = @(
  [ordered]@{ type = 'operating-system'; name = $os.Caption.Trim(); version = $os.Version }
)
$keys = @(
  'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
  'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
)
$seen = @{}
foreach ($item in (Get-ItemProperty $keys -ErrorAction SilentlyContinue)) {
  if (-not $item.DisplayName) { continue }
  $name = [string]$item.DisplayName
  $version = [string]$item.DisplayVersion
  $key = "$name`0$version"
  if ($seen.ContainsKey($key)) { continue }
  $seen[$key] = $true
  $components += [ordered]@{
    type = 'application'; name = $name; version = $version
    supplier = [ordered]@{ name = [string]$item.Publisher }
  }
}
$bom = [ordered]@{ bomFormat = 'CycloneDX'; specVersion = '1.5'; version = 1; components = $components }
$bom | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 (Join-Path $OutputDirectory 'system-inventory.cdx.json')
Get-HotFix | Select-Object HotFixID, InstalledOn, Description |
  Export-Csv -NoTypeInformation -Encoding UTF8 (Join-Path $OutputDirectory 'windows-updates.csv')
Write-Host "納入構成の記録を $OutputDirectory に保存しました。"
