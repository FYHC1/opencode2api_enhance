# DPAPI 诊断脚本集（2026-10-14 事故排查留档）

这批脚本用于排查一类特定故障：**qw2api 插件面板里账号突然全部消失**，但磁盘上的
`vault/<uid>/auth-v2.dat` 明明还在。根因是 Windows DPAPI 主密钥轮换（改密码 / 凭据变化）
导致 `vault/<uid>/Local State` 里的 AES 主密钥解不开，插件读不出凭据。

配合正式工具 `scripts/recover-qw2api-vault.py` 使用：脚本负责「定位问题出在哪一环」，
`recover-qw2api-vault.py` 负责「用旧密码把凭据捞回来」。

## 前置条件

- Python 3 + `impacket`（`pip install impacket`）；`check-mkf.py`、`check-credhist.py`、
  `verify-vault.py`、`summarize-creds.py` 还需要 `cryptography`（`impacket` 会带上）。
- PowerShell 5.1（三个 `.ps1` 依赖 Windows DPAPI 与 `Microsoft\Protect` 目录）。
- 全部脚本默认路径指向 `D:\Program Files\opencode2api\bin\providers\qw2api\data\vault`，
  换机器需自行改路径或用参数覆盖。

## 按排查顺序使用

| 脚本 | 回答的问题 | 是否只读 |
|------|-----------|---------|
| `dpapi-check.ps1` | 当前 Windows 用户能否用 DPAPI 解开各份 `Local State`？（区分「解不开」与「文件丢了」） | 只读 |
| `dpapi-masterkey.ps1` | `%APPDATA%\Microsoft\Protect\<SID>\` 里有几个主密钥、什么时候生成的、`Preferred` 指向谁；并做一次「当前用户加密再解密」自检，确认 DPAPI 本身是否正常 | 只读 |
| `masterkey-guid.ps1` | 每份 `Local State` 的密文头部引用的是**哪个主密钥 GUID**，该 GUID 是否还在磁盘上 | 只读 |
| `check-mkf.py` | 用 impacket 解析主密钥文件结构（头 128 字节 + MasterKey/BackupKey/CredHist/DomainKey 四段长度），核对是否与文件大小吻合，确认解析可靠 | 只读 |
| `check-credhist.py` | 主密钥文件内嵌的 CredHist 有多少字节、是否含历史密码条目（决定「旧密码试错」有没有余地） | 只读 |
| `verify-vault.py` | 修复后**端到端**验证：完全按插件自己的路径（DPAPI → `Local State` → AES-256-GCM → `auth-v2.dat`）能否读出凭据 | 只读 |
| `summarize-creds.py` | 汇总 `recover-qw2api-vault.py` 导出的凭据：邮箱、套餐、access token 过期时间、有无 refreshToken。**刻意不打印 token 本身** | 只读 |
| `cmdarg/main.go` | 另一件事：验证 Go 的 `exec.Command` 把含 `&` 的 URL 交给 `cmd` 会被当命令分隔符截断（`open_url.go` 那次修复的依据之一）。用 `echo` 代替 `start`，不会真弹浏览器 | 只读 |

## 本次事故的结论（供下次少走弯路）

1. **数据不会丢**。插件的 `lsCorrupt` 分支刻意不覆盖解不开的 `Local State`，宿主侧也没有任何
   删除插件账号数据的代码路径（唯一的 `os.RemoveAll(插件目录)` 只在管理页显式删插件时触发）。
2. **旧密码通常就是答案**。即使 `CredHist` 文件缺失、即使内嵌 CredHist 只有 20 字节
   （version + GUID，无历史条目），只要密码与当初一致，impacket 就能用
   `deriveKeysFromUser(sid, password)` 解出主密钥。
3. **判断恢复成功看这两条日志**，不要看「日志是否安静」：`账号已加入池`、
   `vault 对齐：发现新账号，已载入`。`SyncVault` 每 5 秒轮询，写回后同一秒就会入池，
   **不需要重启**。
4. **动手前先整目录备份 `data\`**：`vault\`、`state.json`、`qw2api.log`。
   重新登录/导入不会毁掉旧密文，但备份是唯一的后悔药。

完整经过见 `docs/issue-log/2026-10-14.md`（按项目约定该日志不入库，本地保留）。
