// 扫描执行（并发 worker + 端口分配）。
package manager

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

type portPair struct{ api, socks uint16 }

// run 执行扫描（后台 goroutine）。
func (c *ScanController) run(opts ScanOptions, nodes []ClashNode) {
	defer func() {
		c.mu.Lock()
		c.progress.FinishedMS = time.Now().UnixMilli()
		if c.progress.Status == ScanStopping {
			c.progress.Status = ScanDone
			c.progress.Error = "扫描已中止（已完成部分结果保留）"
		} else {
			c.progress.Status = ScanDone
		}
		c.mu.Unlock()
	}()

	n := len(nodes)
	workers := opts.Concurrency
	if workers > n {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}
	ports, err := c.allocatePorts(opts, workers)
	if err != nil {
		c.mu.Lock()
		c.progress.Status = ScanError
		c.progress.Error = err.Error()
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	c.progress.Total = n
	c.mu.Unlock()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			pair := ports[w]
			workerDir := filepath.Join(c.m.paths.RuntimeDir, fmt.Sprintf("_probe/worker-%02d", w+1))
			for i := w; i < n; i += workers {
				if c.isStopping() {
					return
				}
				start := time.Now()
				res := c.probeNode(opts, nodes[i], pair, workerDir)
				res.LatencyMS = time.Since(start).Milliseconds()
				c.mu.Lock()
				c.progress.Results = append(c.progress.Results, res)
				if !c.stoppingLocked() {
					c.progress.CurrentNode = nodes[i].Name
				}
				c.progress.Current++
				c.mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
}

func (c *ScanController) isStopping() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.progress.Status == ScanStopping
}

func (c *ScanController) stoppingLocked() bool {
	return c.progress.Status == ScanStopping
}

// allocatePorts 按序分配端口对（api+socks 均 ≥1024、互不重复、当前空闲）。
func (c *ScanController) allocatePorts(opts ScanOptions, need int) ([]portPair, error) {
	var pairs []portPair
	used := map[uint16]bool{}
	for offset := 0; offset < 512 && len(pairs) < need; offset++ {
		api := opts.APIPort + uint16(offset)
		socks := opts.SocksPort + uint16(offset)
		if api < 1024 || socks < 1024 || used[api] || used[socks] || api == socks {
			continue
		}
		if !isPortFree(api) || !isPortFree(socks) {
			continue
		}
		used[api], used[socks] = true, true
		pairs = append(pairs, portPair{api: api, socks: socks})
	}
	if len(pairs) < need {
		return nil, fmt.Errorf("无法为并发节点扫描找到 %d 组空闲 API/SOCKS 端口", need)
	}
	return pairs[:need], nil
}
