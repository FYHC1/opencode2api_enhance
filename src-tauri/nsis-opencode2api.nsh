; opencode2api NSIS 安装器钩子
; perMachine 安装到 Program Files 后，安装目录对普通用户只读；
; 而运行时（Rust 壳）需向 exe 旁 bin\ 释放 go core / sing-box 二进制。
; POSTINSTALL 以管理员身份给 $INSTDIR 授予 Users 修改权限（含子目录继承），
; 避免升级后启动即「127.0.0.1 拒绝连接」。
!macro NSIS_HOOK_POSTINSTALL
  ExecWait 'icacls "$INSTDIR" /grant "*S-1-5-32-545:(OI)(CI)M" /T'
!macroend