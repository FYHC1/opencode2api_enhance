// CONC-8（M4 读侧）测试：日志尾部窗口读取（大文件不整读）、.1 轮转旧段合并、
// 多实例并发聚合顺序语义。
package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// setCallLogWindow 注入尾部读取窗口大小（测试结束恢复）。
func setCallLogWindow(t *testing.T, n int64) {
	t.Helper()
	old := callLogTailWindow.Load()
	callLogTailWindow.Store(n)
	t.Cleanup(func() { callLogTailWindow.Store(old) })
}

// padLog 构造一条日志行：pad 填充到目标长度（各条等长，方便精确算窗口边界）。
func padLog(id string, n int) []byte {
	line := `{"req_id":"` + id + `","status":"ok","pad":"` + strings.Repeat("x", n) + `"}`
	return []byte(line)
}

// idsOf 取行切片中可解析记录的 req_id（跳过空白与残行）。
func idsOf(lines [][]byte) []string {
	var ids []string
	for _, line := range lines {
		var rec CallLogRecord
		if json.Unmarshal(line, &rec) == nil {
			ids = append(ids, rec.ReqID)
		}
	}
	return ids
}

// TestReadLogTailWindowOnlyTail：大文件只读末尾窗口——窗口起点落在行中间时
// 首段残行被截掉，窗口外更早的记录不进入结果；字节与文件顺序一致（旧→新）。
func TestReadLogTailWindowOnlyTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.jsonl")
	line := padLog("r00", 6)
	lineLen := len(line) + 1 // 含换行
	var buf []byte
	for i := 0; i < 20; i++ {
		buf = append(buf, padLog(fmt.Sprintf("r%02d", i), 6)...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	// 窗口 = 末尾 5 行（起点落在倒数第 6 行行内）→ 丢弃残段，只回 r15..r19。
	wantWin := int64(lineLen*5) + 3
	lines, ok := readLogTailWindow(path, wantWin)
	if !ok {
		t.Fatal("readLogTailWindow failed")
	}
	got := idsOf(lines)
	want := []string{"r15", "r16", "r17", "r18", "r19"}
	if len(got) != len(want) {
		t.Fatalf("window ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("window ids = %v, want %v", got, want)
		}
	}
	// 窗口起点恰好落在行尾换行符上 → 不丢行（首段为空被跳过）。
	lines, ok = readLogTailWindow(path, int64(lineLen*5))
	if !ok {
		t.Fatal("readLogTailWindow failed")
	}
	if got := idsOf(lines); len(got) != 5 || got[0] != "r15" {
		t.Fatalf("line-boundary window ids = %v, want [r15..r19]", got)
	}
	// 窗口覆盖全文件时整读（不截断首行）。
	lines, ok = readLogTailWindow(path, int64(len(buf))*2)
	if !ok {
		t.Fatal("readLogTailWindow failed")
	}
	if got := idsOf(lines); len(got) != 20 || got[0] != "r00" {
		t.Fatalf("whole-file window ids = %v", got)
	}
}

// TestReadCallLogFileTailWindowBigFile：大文件端到端——小窗口下只回窗口内记录
// （末尾固定条数）；tail 超窗口容量（行超长）时回退整读兜底不丢数据。
func TestReadCallLogFileTailWindowBigFile(t *testing.T) {
	setCallLogWindow(t, 1024)
	path := filepath.Join(t.TempDir(), "big.jsonl")
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, string(padLog(fmt.Sprintf("r%02d", i), 160))) // 每行 ~194B，共 ~6KB
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readCallLogFileTail(path, "src-x", 5)
	if len(got) != 5 {
		t.Fatalf("len=%d, want 5", len(got))
	}
	for i, r := range got {
		want := fmt.Sprintf("r%02d", 25+i)
		if r.ReqID != want || r.Source != "src-x" {
			t.Fatalf("recs[%d] = %+v, want %s/src-x", i, r, want)
		}
	}
	// tail 超窗口：回退整读，全部 30 条回来（顺序保持）。
	got = readCallLogFileTail(path, "src-x", 100)
	if len(got) != 30 || got[0].ReqID != "r00" || got[29].ReqID != "r29" {
		t.Fatalf("fallback len=%d first=%s last=%s", len(got), got[0].ReqID, got[29].ReqID)
	}
}

// TestReadCallLogTailWindowSmallFile：小文件（≤ 窗口）整读，行为与旧实现一致。
func TestReadCallLogTailWindowSmallFile(t *testing.T) {
	setCallLogWindow(t, 1024)
	path := filepath.Join(t.TempDir(), "small.jsonl")
	lines := []string{
		`{"req_id":"r1","ts":"t1","status":"ok"}`,
		`{"req_id":"r2","ts":"t2","status":"ok"}`,
		`{"req_id":"r3","ts":"t3","status":"ok"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs := readCallLogFileTail(path, "s", 2)
	if len(recs) != 2 || recs[0].ReqID != "r2" || recs[1].ReqID != "r3" {
		t.Fatalf("recs = %+v, want [r2 r3]", recs)
	}
}

// TestReadCallLogFileTailRotation：.1 旧段 + 主文件按「旧→新」拼接取尾；
// 缺失 .1 不报错；.1 只保留末尾 tail 条。
func TestReadCallLogFileTailRotation(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "call_log.jsonl")
	oldLines := []string{}
	for i := 1; i <= 10; i++ {
		oldLines = append(oldLines, fmt.Sprintf(`{"req_id":"a%02d","ts":"t%d","status":"ok"}`, i, i))
	}
	if err := os.WriteFile(main+".1", []byte(strings.Join(oldLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newLines := []string{
		`{"req_id":"b01","ts":"t11","status":"ok"}`,
		`{"req_id":"b02","ts":"t12","status":"ok"}`,
	}
	if err := os.WriteFile(main, []byte(strings.Join(newLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 缺失 .1（当前有，先删）→ 只读主文件。
	_ = os.Remove(main + ".1")
	recs := readCallLogFileTail(main, "rot", 100)
	if len(recs) != 2 || recs[0].ReqID != "b01" || recs[1].ReqID != "b02" {
		t.Fatalf("no .1 recs = %+v", recs)
	}
	// 有 .1：tail=5 → 旧段末尾 a08..a10（.1 只留最后 5 条）+ 主文件 b01 b02。
	_ = os.WriteFile(main+".1", []byte(strings.Join(oldLines, "\n")+"\n"), 0o644)
	recs = readCallLogFileTail(main, "rot", 5)
	want := []string{"a08", "a09", "a10", "b01", "b02"}
	if len(recs) != 5 {
		t.Fatalf("rotation tail len=%d recs=%+v, want %v", len(recs), recs, want)
	}
	for i := range want {
		if recs[i].ReqID != want[i] {
			t.Fatalf("rotation recs = %+v, want %v", recs, want)
		}
	}
	// tail 足够大 → 全量 .1 + 主文件顺序。
	recs = readCallLogFileTail(main, "rot", 100)
	if len(recs) != 12 || recs[0].ReqID != "a01" || recs[11].ReqID != "b02" {
		t.Fatalf("rotation full len=%d", len(recs))
	}
}

// TestReadCallLogParallelTieBreak：并发聚合下同时间戳记录仍按
// 「网关 → 实例（ListInstances 名称序）」稳定排序。
func TestReadCallLogParallelTieBreak(t *testing.T) {
	m := newTestManager(t)
	writeGateWayLog(t, m, []string{
		`{"req_id":"gA","ts":"2026-08-05T10:00:00+08:00","status":"ok"}`,
		`{"req_id":"gB","ts":"2026-08-05T10:00:00+08:00","status":"ok"}`,
		`{"req_id":"g8","ts":"2026-08-05T10:08:00+08:00","status":"ok"}`,
	})
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("inst%02d", i)
		_ = m.AddInstance(Instance{Name: name, Port: uint16(20000 + i), Node: "n", Password: "sk"})
		markRunning(t, m, name)
		ts := fmt.Sprintf("2026-08-05T10:%02d:00+08:00", i)
		writeInstanceLog(t, m, name, []string{
			fmt.Sprintf(`{"req_id":"%s","ts":"%s","status":"ok","model":"m"}`, name, ts),
		})
	}
	// 共 15 条：gA gB(10:00) → inst01(10:01)…inst07(10:07) → g8+inst08(10:08，
	// 网关在前) → inst09…inst12。
	recs := m.ReadCallLog(100)
	if len(recs) != 15 {
		t.Fatalf("len=%d, want 15", len(recs))
	}
	if recs[0].ReqID != "gA" || recs[1].ReqID != "gB" || recs[2].ReqID != "inst01" {
		t.Fatalf("tie-break head = %s,%s,%s; want gA,gB,inst01",
			recs[0].ReqID, recs[1].ReqID, recs[2].ReqID)
	}
	if recs[8].ReqID != "inst07" {
		t.Fatalf("recs[8] = %s, want inst07", recs[8].ReqID)
	}
	if recs[9].ReqID != "g8" || recs[10].ReqID != "inst08" {
		t.Fatalf("10:08 时间戳 tie-break = %s,%s; want g8,inst08 (网关在前)",
			recs[9].ReqID, recs[10].ReqID)
	}
	if recs[14].ReqID != "inst12" {
		t.Fatalf("recs[14] = %s, want inst12", recs[14].ReqID)
	}
	for i, r := range recs {
		if strings.HasPrefix(r.ReqID, "g") {
			if r.Source != "" {
				t.Fatalf("gateway recs[%d] source=%q, want empty", i, r.Source)
			}
		} else if r.Source != r.ReqID {
			t.Fatalf("instance recs[%d] source=%q, want %q", i, r.Source, r.ReqID)
		}
	}
	// 并行结果与串行读取 + 同样稳定排序后逐条一致（-race 下验证并发收集无竞态）。
	var all []CallLogRecord
	all = append(all, readCallLogFilePart(m.CallLogPath(), "", callLogAggregateTail)...)
	for _, inst := range m.ListInstances() {
		if inst.Status.State != "Running" {
			continue
		}
		all = append(all, readCallLogFilePart(m.InstanceCallLogPath(inst.Name), inst.Name, callLogAggregateTail)...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return callLogTime(all[i].TS).Before(callLogTime(all[j].TS))
	})
	if len(all) != len(recs) {
		t.Fatalf("serial parts len=%d, want %d", len(all), len(recs))
	}
	for i := range all {
		if all[i].ReqID != recs[i].ReqID {
			t.Fatalf("parts[%d]=%s vs parallel %s", i, all[i].ReqID, recs[i].ReqID)
		}
	}
}

// TestReadCallLogParallelIncludesRotation：ReadCallLog 聚合路径同样合并
// 网关与实例的 .1 旧段（时间排序后展开）。
func TestReadCallLogParallelIncludesRotation(t *testing.T) {
	m := newTestManager(t)
	gwPath := m.CallLogPath()
	if err := os.MkdirAll(filepath.Dir(gwPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldGW := `{"req_id":"g0","ts":"2026-08-05T09:00:00+08:00","status":"ok"}`
	if err := os.WriteFile(gwPath+".1", []byte(oldGW+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGateWayLog(t, m, []string{
		`{"req_id":"g2","ts":"2026-08-05T10:30:00+08:00","status":"ok"}`,
	})
	_ = m.AddInstance(Instance{Name: "solo-a", Port: 14400, Node: "n1", Password: "sk"})
	markRunning(t, m, "solo-a")
	writeInstanceLog(t, m, "solo-a", []string{
		`{"req_id":"a1","ts":"2026-08-05T10:15:00+08:00","status":"ok"}`,
	})
	if err := os.WriteFile(filepath.Join(m.paths.RuntimeDir, sanitizeInstanceName("solo-a"), "call_log.jsonl.1"),
		[]byte(`{"req_id":"a0","ts":"2026-08-05T09:30:00+08:00","status":"ok"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs := m.ReadCallLog(100)
	want := []string{"g0", "a0", "a1", "g2"}
	if len(recs) != 4 {
		t.Fatalf("len=%d recs=%+v, want %v", len(recs), recs, want)
	}
	for i := range want {
		if recs[i].ReqID != want[i] {
			t.Fatalf("order=%+v, want %v", recs, want)
		}
	}
}

// TestClearCallLogRemovesRotatedFile：清空日志同时删除 .1 旧段（避免旧段残留重返日志页）。
func TestClearCallLogRemovesRotatedFile(t *testing.T) {
	m := newTestManager(t)
	writeGateWayLog(t, m, []string{`{"req_id":"g1","ts":"t","status":"ok"}`})
	if err := os.WriteFile(m.CallLogPath()+".1", []byte(`{"req_id":"old","ts":"t","status":"ok"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.ClearCallLog(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, p := range []string{m.CallLogPath(), m.CallLogPath() + ".1"} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s should be gone", p)
		}
	}
}