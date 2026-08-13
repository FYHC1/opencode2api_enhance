package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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