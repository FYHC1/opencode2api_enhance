// Package manager 承载原 Tauri 管理域（35 command）的 Go 移植：
// 实例生命周期 / 端口 / clash 解析 / sing-box 配置 / 探针扫描 / 网关 / 统计聚合 /
// 调用日志 / 应用配置。HTTP 层经 /api/admin/* 暴露（P4）。
//
// 目录约定（与 Rust 侧一致）：
//
//	dataDir    = OPCODE2API_DATA_DIR（非空）| <UserConfigDir>/opencode2api-manager
//	instances  = dataDir/instances.json
//	runtime    = dataDir/runtime/…（各实例目录 + _unified-gateway + _probe）
//	binDir     = <当前可执行文件目录>/bin（内嵌/外置二进制）
package manager

import (
	"os"
	"path/filepath"
)

// DefaultDataDir 计算管理数据目录（Rust config.rs 语义）。
func DefaultDataDir() string {
	if dir := os.Getenv("OPCODE2API_DATA_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		return dir
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	dir := filepath.Join(base, "opencode2api-manager")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// Paths 是管理器常用的路径集合。
type Paths struct {
	DataDir    string
	Config     string // dataDir/config.json
	Instances  string // dataDir/instances.json
	RuntimeDir string // dataDir/runtime
	BinDir     string // <exe>/bin
}

// ResolvePaths 计算全部路径。dataDir 为空时用 DefaultDataDir。
func ResolvePaths(dataDir string) Paths {
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	exeDir := "."
	if p, e := os.Executable(); e == nil {
		exeDir = filepath.Dir(p)
	}
	// 约定：Go core 由壳（Rust）释放到 <壳exe>/bin/ 下运行，sing-box.exe 与 core 同目录。
	// 因此 binDir 默认 = core 所在目录（filepath.Dir(os.Executable())），无需再拼 "bin"。
	binDir := exeDir
	// 兼容旧形态：若 core 目录下没有 sing-box，但父级存在 bin/ 子目录，则回退（如单文件 Web 直跑）。
	if _, err := os.Stat(filepath.Join(exeDir, "sing-box.exe")); err != nil {
		alt := filepath.Join(exeDir, "bin")
		if fi, e := os.Stat(filepath.Join(alt, "sing-box.exe")); e == nil && !fi.IsDir() {
			binDir = alt
		}
	}
	return Paths{
		DataDir:    dataDir,
		Config:     filepath.Join(dataDir, "config.json"),
		Instances:  filepath.Join(dataDir, "instances.json"),
		RuntimeDir: filepath.Join(dataDir, "runtime"),
		BinDir:     binDir,
	}
}

// RuntimeDirOf 返回某实例的运行目录（runtime/<name>）。
func (p Paths) RuntimeDirOf(name string) string {
	return filepath.Join(p.RuntimeDir, name)
}

// prepareRuntimes 确保 runtime 目录存在。
func (p Paths) prepareRuntimes() error {
	if err := os.MkdirAll(p.RuntimeDir, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(p.RuntimeDir, "_unified-gateway"), 0o755)
}
