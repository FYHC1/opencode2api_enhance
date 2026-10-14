Add-Type -AssemblyName System.Security
$ErrorActionPreference = 'Continue'
Add-Type -AssemblyName System.Windows.Extensions -ErrorAction SilentlyContinue

$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
"当前 SID: $sid"
""

"=== %APPDATA%\Microsoft\Protect\<SID>\ 下的 DPAPI 主密钥文件 ==="
$prot = Join-Path $env:APPDATA "Microsoft\Protect\$sid"
if (Test-Path $prot) {
  Get-ChildItem $prot -Force -Recurse | Select-Object FullName, Length, CreationTime, LastWriteTime | Sort-Object LastWriteTime | Format-Table -AutoSize
}
else { "目录不存在: $prot" }

""
"=== 对照：Chrome profile 的 Local State 主密钥能否被当前用户 DPAPI 解开 ==="
$chromeLs = "D:\Program Files\opencode2api\bin\providers\qw2api\data\chrome-autoreg\Local State"
if (Test-Path $chromeLs) {
  $raw = Get-Content $chromeLs -Raw
  $m = [regex]::Match($raw, '"os_crypt"\s*:\s*\{[^}]*?"encrypted_key"\s*:\s*"([^"]+)"')
  if ($m.Success) {
    $b = [Convert]::FromBase64String($m.Groups[1].Value)
    $prefix = [System.Text.Encoding]::ASCII.GetString($b[0..4])
    [byte[]]$blob = $b[5..($b.Length - 1)]
    try {
      $p = [System.Security.Cryptography.ProtectedData]::Unprotect($blob, $null, 'CurrentUser')
      "[Chrome Local State / CurrentUser=OK] 明文 $($p.Length) 字节 (前缀=$prefix)"
    }
    catch { "[Chrome Local State / CurrentUser=失败] $($_.Exception.Message)" }
  }
  else { "未匹配到 encrypted_key" }
}

""
"=== 对照：Chrome profile 里其它 DPAPI 加密文件（Login Data 的 password_value）==="
$login = "D:\Program Files\opencode2api\bin\providers\qw2api\data\chrome-autoreg\Default\Login Data"
if (Test-Path $login) { "存在: $login ($((Get-Item $login).Length) 字节)" } else { "无 Login Data" }

""
"=== 用户配置文件时间 ==="
Get-Item "$env:USERPROFILE\NTUSER.DAT" -Force -ErrorAction SilentlyContinue | Select-Object FullName, LastWriteTime | Format-Table -AutoSize
