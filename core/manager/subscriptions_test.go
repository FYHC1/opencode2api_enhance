package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// T3: 订阅源列表 CRUD + 迁移 + 立即拉取（含 HTTP 契约）。
func TestSubscriptionsCRUD(t *testing.T) {
	m := New(t.TempDir())

	// 空列表
	list := m.loadSubscriptions()
	if len(list) != 0 {
		t.Fatalf("initial list = %+v", list)
	}

	// 新增两条
	if err := m.AddSubscription("http://a.example.com/sub", 30, TargetPool); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := m.AddSubscription("http://b.example.com/sub", 60, TargetSolo); err != nil {
		t.Fatalf("add b: %v", err)
	}
	// 重复 URL 报错
	if err := m.AddSubscription("http://a.example.com/sub", 5, TargetSolo); err == nil {
		t.Fatal("duplicate url should error")
	}
	// 非法目标
	if err := m.AddSubscription("http://c.example.com/sub", 5, SubscriptionTarget("bogus")); err == nil {
		t.Fatal("bogus target should error")
	}
	// 空 URL
	if err := m.AddSubscription("   ", 5, TargetSolo); err == nil {
		t.Fatal("blank url should error")
	}

	list = m.loadSubscriptions()
	if len(list) != 2 {
		t.Fatalf("list after add = %+v", list)
	}
	if list[0].Target != TargetPool || list[1].Target != TargetSolo {
		t.Fatalf("targets = %q / %q", list[0].Target, list[1].Target)
	}

	// 删除
	removed, err := m.RemoveSubscription("http://a.example.com/sub")
	if err != nil || !removed {
		t.Fatalf("remove a: removed=%v err=%v", removed, err)
	}
	removed, err = m.RemoveSubscription("http://nonexistent.example.com/x")
	if err != nil || removed {
		t.Fatalf("remove nonexistent: removed=%v err=%v", removed, err)
	}
	list = m.loadSubscriptions()
	if len(list) != 1 || list[0].URL != "http://b.example.com/sub" {
		t.Fatalf("list after remove = %+v", list)
	}
}

// T3: 旧 config 单条订阅迁移并入列表。
func TestSubscriptionsMigrateFromConfig(t *testing.T) {
	m := New(t.TempDir())
	_ = m.ConfigSet("subscribe_url", "http://legacy.example.com/sub")
	_ = m.ConfigSet("subscribe_interval_min", "15")

	// 迁移触发：load 并入旧配置为第一条，并落盘
	list := m.loadSubscriptions()
	if len(list) != 1 {
		t.Fatalf("migrated list = %+v", list)
	}
	if list[0].URL != "http://legacy.example.com/sub" || list[0].IntervalMin != 15 || list[0].Target != TargetSolo {
		t.Fatalf("migrated item = %+v", list[0])
	}
	// 再次读取不再重复（已落盘优先）
	list2 := m.loadSubscriptions()
	if len(list2) != 1 {
		t.Fatalf("second load = %+v", list2)
	}
}

// T3: 立即拉取单条订阅（真实 HTTP 源 → 独享实例）。
func TestSubscriptionsImportNowHTTP(t *testing.T) {
	m := New(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("vless://uuid@x.example.com:443?security=tls#X"))
	}))
	defer srv.Close()

	// 登记为 pool-only（仅节点池）
	if err := m.AddSubscription(srv.URL, 0, TargetPoolOnly); err != nil {
		t.Fatalf("add: %v", err)
	}
	n, label, err := m.ImportSubscriptionNow(srv.URL)
	if err != nil {
		t.Fatalf("import now: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported = %d, want 1", n)
	}
	if label != "节点池" {
		t.Fatalf("label = %q, want 节点池", label)
	}
	// pool-only 不应建实例
	if len(m.ListInstances()) != 0 {
		t.Fatalf("pool-only must not create instances, got %d", len(m.ListInstances()))
	}
	// 节点池缓存应有 1 条
	if len(m.loadSubscriptionCache()) != 1 {
		t.Fatalf("cache = %+v", m.loadSubscriptionCache())
	}

	// 改为 solo → 立即拉取应建实例
	if err := m.AddSubscription(srv.URL, 0, TargetSolo); err == nil {
		t.Fatal("duplicate add should error (solo)")
	}
	_, _ = m.RemoveSubscription(srv.URL)
	if err := m.AddSubscription(srv.URL, 0, TargetSolo); err != nil {
		t.Fatalf("re-add solo: %v", err)
	}
	n, label, err = m.ImportSubscriptionNow(srv.URL)
	if err != nil {
		t.Fatalf("import solo: %v", err)
	}
	if label != "独享" {
		t.Fatalf("label = %q, want 独享", label)
	}
	insts := m.ListInstances()
	if len(insts) != 1 {
		t.Fatalf("solo must create 1 instance, got %d", len(insts))
	}
	if insts[0].JoinGateway {
		t.Fatal("solo instance must not join gateway")
	}
}

// T3: HTTP 契约——list 返回订阅源数组。
func TestSubscriptionsListHandlerHTTP(t *testing.T) {
	m := New(t.TempDir())
	_ = m.AddSubscription("http://h.example.com/sub", 10, TargetSolo)
	h := m.SubscriptionsListHandler()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 {
		t.Fatalf("list code = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"url":"http://h.example.com/sub"`) {
		t.Fatalf("list body = %s", rec.Body.String())
	}
	// add 契约
	ah := m.SubscriptionsAddHandler()
	rec2 := httptest.NewRecorder()
	ah(rec2, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"url":"http://h2.example.com/s","interval_min":5,"target":"pool"}`)))
	if rec2.Code != 200 {
		t.Fatalf("add code = %d, body=%s", rec2.Code, rec2.Body.String())
	}
	if len(m.loadSubscriptions()) != 2 {
		t.Fatalf("after add = %+v", m.loadSubscriptions())
	}
}

// V2: 删除订阅前统计使用中实例数（按订阅缓存节点名匹配实例 Node）。
func TestCountInstancesForGroup(t *testing.T) {
	m := New(t.TempDir())
	// 订阅缓存：分组 "机场A" 两个节点 + 分组 "机场B" 一个节点
	_ = m.saveSubscriptionCache([]SubscribeNode{
		{Name: "HK-01", Group: "机场A"},
		{Name: "JP-02", Group: "机场A"},
		{Name: "US-03", Group: "机场B"},
	})
	// 未运行（Stopped）实例也属于"使用中"（占着节点）
	_ = m.AddInstance(Instance{Name: "hk1", Node: "HK-01", Port: 28101, JoinGateway: false})
	_ = m.AddInstance(Instance{Name: "jp2", Node: "JP-02", Port: 28102, JoinGateway: false})
	_ = m.AddInstance(Instance{Name: "us3", Node: "US-03", Port: 28103, JoinGateway: false})
	// jp2 置为运行中
	if inst, ok := m.FindInstance("jp2"); ok {
		inst.Status = StatusRunning()
		_ = m.UpdateInstance(inst)
	}

	running, stopped := m.countInstancesForGroup("机场A")
	if running != 1 || stopped != 1 {
		t.Fatalf("机场A running=%d stopped=%d, want 1/1", running, stopped)
	}
	running, stopped = m.countInstancesForGroup("机场B")
	if running != 0 || stopped != 1 {
		t.Fatalf("机场B running=%d stopped=%d, want 0/1", running, stopped)
	}
	running, stopped = m.countInstancesForGroup("不存在")
	if running != 0 || stopped != 0 {
		t.Fatalf("不存在 running=%d stopped=%d, want 0/0", running, stopped)
	}
}

// ---------- CONC-7 M6：每源独立调度 ----------

const testSubNodeBody = "vless://uuid@x.example.com:443?security=tls#X"

// TestSubscriptionWaitPerSource 结构性断言：每源循环睡自己的 IntervalMin
// （避免真实长 sleep）。
func TestSubscriptionWaitPerSource(t *testing.T) {
	if got := subscriptionWaitOf(SubscriptionSource{IntervalMin: 1}); got != time.Minute {
		t.Fatalf("interval 1 = %v, want 1m", got)
	}
	if got := subscriptionWaitOf(SubscriptionSource{IntervalMin: 1440}); got != 24*time.Hour {
		t.Fatalf("interval 1440 = %v, want 24h", got)
	}
}

// TestSubscriptionSourcesIndependentLoops 每源独立循环：慢源首轮拉取阻塞期间，
// 快源必须完成自己的首轮拉取（串行整轮实现会被慢源拖住）。
func TestSubscriptionSourcesIndependentLoops(t *testing.T) {
	m := New(t.TempDir())
	var aMu, bMu sync.Mutex
	aCalls, bCalls := 0, 0
	aEntered := make(chan struct{}, 4)
	aRelease := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(aRelease) }) }

	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aMu.Lock()
		aCalls++
		aMu.Unlock()
		select {
		case aEntered <- struct{}{}:
		default:
		}
		<-aRelease // 慢源：首轮挂起直到放行
		_, _ = w.Write([]byte(testSubNodeBody))
	}))
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bMu.Lock()
		bCalls++
		bMu.Unlock()
		_, _ = w.Write([]byte(testSubNodeBody))
	}))
	defer srvA.Close()
	defer srvB.Close()
	_ = m.AddSubscription(srvA.URL, 1, TargetPoolOnly)
	_ = m.AddSubscription(srvB.URL, 1, TargetPoolOnly)

	stop := m.RunAllSubscriptionLoop()
	defer stop()
	defer releaseNow() // 先放行阻塞的 fetch，再停循环（stop 会等循环退出）

	<-aEntered // 慢源已进入拉取（阻塞中）
	// 慢源阻塞期间，快源应完成自己的首轮拉取。
	deadline := time.Now().Add(3 * time.Second)
	for {
		bMu.Lock()
		n := bCalls
		bMu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("快源首轮拉取被慢源拖住（未每源独立调度）")
		}
		time.Sleep(10 * time.Millisecond)
	}
	releaseNow()
}

// TestSubscriptionFetchConcurrencyCapped 并发拉取 ≤ 门控（4）：
// 6 个源同时到点，同时只有 4 个在途。
func TestSubscriptionFetchConcurrencyCapped(t *testing.T) {
	m := New(t.TempDir())
	const total = 6
	entered := make(chan struct{}, total)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	var gateMu sync.Mutex
	active, maxActive := 0, 0

	handler := func(w http.ResponseWriter, r *http.Request) {
		gateMu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		gateMu.Unlock()
		entered <- struct{}{}
		<-release
		gateMu.Lock()
		active--
		gateMu.Unlock()
		_, _ = w.Write([]byte(testSubNodeBody))
	}
	for i := 0; i < total; i++ {
		srv := httptest.NewServer(http.HandlerFunc(handler))
		defer srv.Close()
		_ = m.AddSubscription(srv.URL, 1, TargetPoolOnly)
	}

	stop := m.RunAllSubscriptionLoop()
	defer stop()
	defer releaseNow() // 先放行阻塞的 fetch，再停循环（stop 会等循环退出）

	// 首轮 6 源到点：门控应恰好放 4 个进 fetch。
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("第 %d 个拉取未进入（门控异常）", i)
		}
	}
	// 未放行前不应出现第 5 个并发在途。
	select {
	case <-entered:
		t.Fatal("并发拉取超过门控上限 4")
	case <-time.After(300 * time.Millisecond):
	}
	releaseNow()
	// 放行后余下 2 个也应各自完成首轮。
	for i := 0; i < total-4; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("放行后第 %d 个拉取未完成", i)
		}
	}
	if maxActive != 4 {
		t.Fatalf("最大并发拉取数 = %d, want 4", maxActive)
	}
}

// TestSubscriptionLoopStartsOnce 双入口唯一启动：StartSubscribeLoop 与
// RunAllSubscriptionLoop 只启动一套调度，不会并发拉同一 URL。
func TestSubscriptionLoopStartsOnce(t *testing.T) {
	m := New(t.TempDir())
	var mu sync.Mutex
	calls := 0
	firstEntered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			close(firstEntered)
			<-release
		}
		_, _ = w.Write([]byte(testSubNodeBody))
	}))
	defer srv.Close()
	_ = m.AddSubscription(srv.URL, 1, TargetPoolOnly)

	stop := m.RunAllSubscriptionLoop()
	m.StartSubscribeLoop() // 旧入口：应被唯一启动保护挡住
	defer stop()
	defer releaseNow() // 先放行阻塞的 fetch，再停循环（stop 会等循环退出）

	<-firstEntered // 首次拉取在途（阻塞）
	// 观察窗口：不得出现第二次在途拉取（双循环并发会发出第二个请求）。
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n > 1 {
		t.Fatalf("双入口启动了双循环，同一 URL 并发拉取 %d 次", n)
	}
	releaseNow()
}