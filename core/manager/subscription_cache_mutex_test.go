package manager

// M7 订阅缓存并发单测：不同分组的订阅并发导入后两组节点都必须保留
// （回归：load→merge→写无互斥时后写覆盖先写，一组节点从缓存消失）；-race 下须干净。
// 另断言原子落盘：目录不留 subscription-*.tmp 残留，缓存文件始终为完整 JSON。

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestSubscriptionCacheConcurrentGroupsBothSurvive(t *testing.T) {
	m := New(t.TempDir())
	const perGroup = 200
	mkGroup := func(prefix string) []SubscribeNode {
		out := make([]SubscribeNode, 0, perGroup)
		for i := 0; i < perGroup; i++ {
			out = append(out, SubscribeNode{
				Name:     fmt.Sprintf("%s-%03d", prefix, i),
				Server:   fmt.Sprintf("10.%d.%d.%d", 1+i%5, 2+i%7, 3+i%3),
				Port:     443,
				NodeType: "trojan",
				Group:    "组" + prefix,
			})
		}
		return out
	}
	var wg sync.WaitGroup
	for _, prefix := range []string{"A", "B"} {
		prefix := prefix
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 5; round++ {
				if err := m.saveSubscriptionCacheGrouped(mkGroup(prefix)); err != nil {
					t.Errorf("save group %s: %v", prefix, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	cache := m.loadSubscriptionCache()
	if len(cache) != perGroup*2 {
		t.Fatalf("cache = %d nodes, want %d (两组都要在)", len(cache), perGroup*2)
	}
	names := map[string]bool{}
	for _, n := range cache {
		names[n.Name] = true
	}
	for i := 0; i < perGroup; i++ {
		if !names[fmt.Sprintf("A-%03d", i)] {
			t.Fatalf("group A node A-%03d missing after concurrent save", i)
		}
		if !names[fmt.Sprintf("B-%03d", i)] {
			t.Fatalf("group B node B-%03d missing after concurrent save", i)
		}
	}
	// 原子落盘：目录无 subscription-*.tmp 残留
	entries, err := os.ReadDir(m.paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "subscription-") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
	}
}