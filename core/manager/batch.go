// 批量操作（Rust commands.rs batch_add/start/stop/delete 移植）。
// 并行启停用 worker 池；批量添加按节点去重、端口冲突自动 +1。
package manager

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
)

const (
	batchStartWorkers = 4
	batchStopWorkers  = 8
	defaultBasePort   = 18100
)

// instanceBasePort 实例 API 端口起始：优先环境变量 OPCODE2API_INSTANCE_BASE_PORT
// （便携测试/多开隔离用），否则默认 18100（正式版语义，与 Rust 一致）。
func instanceBasePort() uint16 {
	if s := os.Getenv("OPCODE2API_INSTANCE_BASE_PORT"); s != "" {
		if n := parsePositiveInt(s); n > 0 && n < 65536 {
			return uint16(n)
		}
	}
	return defaultBasePort
}

// BatchAddResult 批量添加结果。
type BatchAddResult struct {
	Added   []string `json:"added"`
	Skipped []string `json:"skipped"`
}

// genSkKey 生成 sk- 密钥（16 字节 hex）。
func genSkKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "sk-" + hex.EncodeToString(b)
}

// BatchAdd 把节点批量添加为实例：
//   - 同名实例已存在 → 跳过；
//   - 端口冲突 → 从 basePort 起 +1 找空闲（实例表 + 本机端口双重判断）；
//   - 名称：useNodeName 用节点名，否则「实例N」（N 从 1 起递增到不冲突）。
func (m *Manager) BatchAdd(nodes []ClashNode, basePort uint16, useNodeName bool, namePrefix string) BatchAddResult {
	if basePort == 0 {
		basePort = instanceBasePort()
	}
	result := BatchAddResult{Added: []string{}, Skipped: []string{}}
	existing := m.ListInstances()
	haveName := map[string]bool{}
	haveNode := map[string]bool{} // 按节点去重（同一节点只允许例化一次）
	for _, e := range existing {
		haveName[e.Name] = true
		haveNode[e.Node] = true
	}
	next := 1
	for _, node := range nodes {
		if haveNode[node.Name] {
			result.Skipped = append(result.Skipped, node.Name)
			continue
		}
		name := node.Name
		if !useNodeName || name == "" {
			for {
				name = fmt.Sprintf("%s实例%d", namePrefix, next)
				next++
				if !haveName[name] {
					break
				}
			}
		}
		if haveName[name] {
			result.Skipped = append(result.Skipped, name)
			continue
		}
		haveName[name] = true

		// 找空闲端口（实例端口 + 对应 sing-box 端口双重判断）
		port := basePort
		for m.isPortUsedByInstance(port) || m.isPortUsedByInstance(port+10000) || !isPortFree(port) {
			port++
		}
		inst := Instance{
			Name:        name,
			Port:        port,
			Node:        node.Name,
			Password:    genSkKey(),
			IP:          node.Server + ":" + itoa(node.Port),
			SingboxPort: port + 10000,
			JoinGateway: false,
		}
		if err := m.AddInstance(inst); err != nil {
			result.Skipped = append(result.Skipped, name)
			continue
		}
		haveNode[node.Name] = true
		result.Added = append(result.Added, name)
	}
	return result
}

// BatchStart 并行启动实例（4 worker）。
func (m *Manager) BatchStart(runner Runner, names []string) map[string]error {
	out := map[string]error{}
	var mu sync.Mutex
	sem := make(chan struct{}, batchStartWorkers)
	var wg sync.WaitGroup
	for _, n := range names {
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			err := m.StartInstance(runner, n)
			<-sem
			mu.Lock()
			out[n] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// BatchStop 并行停止实例（8 worker，含运行中判定）。
func (m *Manager) BatchStop(runner Runner, names []string) map[string]error {
	out := make(map[string]error)
	var mu sync.Mutex
	sem := make(chan struct{}, batchStopWorkers)
	var wg sync.WaitGroup
	for _, n := range names {
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			err := m.StopInstance(runner, n)
			<-sem
			mu.Lock()
			out[n] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// BatchDelete 批量删除（先停后删，全部成员参与）。
func (m *Manager) BatchDelete(runner Runner, names []string) map[string]error {
	out := make(map[string]error)
	for _, n := range names {
		out[n] = m.RemoveInstanceAlive(runner, n)
	}
	return out
}

// StopAllInstances 停止全部运行实例（缓存停驶，重启池/data_clean 用）。
func (m *Manager) StopAllInstances(runner Runner) []string {
	names := []string{}
	for _, inst := range m.ListInstances() {
		names = append(names, inst.Name)
	}
	_ = m.BatchStop(runner, names)
	return names
}
