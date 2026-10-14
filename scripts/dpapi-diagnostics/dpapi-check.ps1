Add-Type -AssemblyName System.Security
$ErrorActionPreference = 'Continue'

$id = [System.Security.Principal.WindowsIdentity]::GetCurrent()
"当前用户 : $($id.Name)"
"当前 SID : $($id.User.Value)"
""

function Try-Unprotect([string]$path) {
  if (-not (Test-Path $path)) { "  [缺失] $path"; return }
  try {
    $j = Get-Content $path -Raw | ConvertFrom-Json
    if (-not ($j.os_crypt -and $j.os_crypt.encrypted_key)) { "  [无 os_crypt.encrypted_key] $path"; return }
    $b = [Convert]::FromBase64String($j.os_crypt.encrypted_key)
    $prefix = [System.Text.Encoding]::ASCII.GetString($b[0..4])
    [byte[]]$blob = $b[5..($b.Length - 1)]
    try {
      $p = [System.Security.Cryptography.ProtectedData]::Unprotect($blob, $null, 'CurrentUser')
      "  [CurrentUser=OK] 明文 $($p.Length) 字节 | 前缀=$prefix | $path"
    }
    catch {
      "  [CurrentUser=失败] $($_.Exception.Message) | 前缀=$prefix | $path"
      try {
        $p2 = [System.Security.Cryptography.ProtectedData]::Unprotect($blob, $null, 'LocalMachine')
        "  [LocalMachine=OK] 明文 $($p2.Length) 字节 | $path"
      }
      catch { "  [LocalMachine=失败] $($_.Exception.Message) | $path" }
    }
  }
  catch { "  [读取异常] $path : $($_.Exception.Message)" }
}

"D:\Program Files\opencode2api\bin\providers\qw2api\data\vault" -as [void]
"=== vault 内 6 份 Local State ==="
Get-ChildItem "D:\Program Files\opencode2api\bin\providers\qw2api\data\vault" -Directory |
  ForEach-Object { Try-Unprotect (Join-Path $_.FullName 'Local State') }

""
"=== 对照：自动注册 Chrome profile 的 Local State ==="
Try-Unprotect "D:\Program Files\opencode2api\bin\providers\qw2api\data\chrome-autoreg\Local State"
