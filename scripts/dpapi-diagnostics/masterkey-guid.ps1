$ErrorActionPreference = 'Continue'
$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$prot = "C:\Users\ASUS\AppData\Roaming\Microsoft\Protect\$sid"
$have = @{}
Get-ChildItem $prot -Force -File | Where-Object { $_.Name -ne 'Preferred' } | ForEach-Object {
  $have[$_.Name.ToLower()] = $_.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss')
}

function Get-KeyGuid([byte[]]$blob) {
  $d1 = [BitConverter]::ToUInt32($blob, 24)
  $d2 = [BitConverter]::ToUInt16($blob, 28)
  $d3 = [BitConverter]::ToUInt16($blob, 30)
  $rest = ($blob[32..39] | ForEach-Object { $_.ToString('x2') }) -join ''
  return ('{0:x8}-{1:x4}-{2:x4}-{3}-{4}' -f $d1, $d2, $d3, $rest.Substring(0, 4), $rest.Substring(4))
}

function Probe([string]$path, [string]$tag) {
  if (-not (Test-Path $path)) { "  [missing] $path"; return }
  $raw = Get-Content $path -Raw
  $m = [regex]::Match($raw, '"encrypted_key"\s*:\s*"([^"]+)"')
  if (-not $m.Success) { "  [no encrypted_key] $path"; return }
  [byte[]]$b = [Convert]::FromBase64String($m.Groups[1].Value)
  [byte[]]$blob = $b[5..($b.Length - 1)]
  $g = Get-KeyGuid $blob
  $k = $g.ToLower()
  if ($have.ContainsKey($k)) { "OK   $tag -> key=$g presentOnDisk(written $($have[$k]))" }
  else { "FAIL $tag -> key=$g NOT-on-disk" }
}

"=== vault Local State: which DPAPI master key does each reference? ==="
Get-ChildItem "D:\Program Files\opencode2api\bin\providers\qw2api\data\vault" -Directory |
  ForEach-Object { Probe (Join-Path $_.FullName 'Local State') $_.Name.Substring(0, 8) }

""
"=== control: chrome-autoreg Local State ==="
Probe "D:\Program Files\opencode2api\bin\providers\qw2api\data\chrome-autoreg\Local State" 'chromels'

""
"=== master key files on disk ==="
Get-ChildItem $prot -Force -File | ForEach-Object { "  {0}  {1}" -f $_.Name, $_.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss') }