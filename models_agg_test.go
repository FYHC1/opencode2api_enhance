package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/6Kmfi6HP/opencode2api/core/aggregator"
	"github.com/6Kmfi6HP/opencode2api/core/contract"
)

func TestAppendOtherFreeModels(t *testing.T) {
	agg := aggregator.New()
	agg.Register(&aggModelVendor{id: "opencode", models: []string{"m-shared", "m-opc-free"}})
	agg.Register(&aggModelVendor{id: "windsurf", models: []string{"m-shared", "swe-1-6-slow"}})
	if err := agg.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	base := []ModelInfo{
		{ID: "m-shared"},
		{ID: "m-opc-free"},
	}
	out := appendOtherFreeModels(base, agg)

	byID := map[string]string{}
	for _, m := range out {
		byID[m.ID] = m.OwnedBy
	}
	if _, ok := byID["m-shared"]; !ok {
		t.Fatal("base m-shared must remain")
	}
	if v, ok := byID["windsurf/m-shared"]; !ok || v != "windsurf" {
		t.Fatalf("collided name windsurf/m-shared missing, got %v", byID)
	}
	if v, ok := byID["swe-1-6-slow"]; !ok || v != "windsurf" {
		t.Fatalf("unique other-vendor model missing: %v", byID)
	}
	if _, ok := byID["m-opc-free"]; !ok {
		t.Fatal("opencode free model must remain without prefix")
	}
}

func TestAppendOtherFreeModelsSingleVendorNoop(t *testing.T) {
	agg := aggregator.New()
	agg.Register(&aggModelVendor{id: "opencode", models: []string{"m1"}})
	_ = agg.Refresh(context.Background())

	base := []ModelInfo{{ID: "m1"}}
	if out := appendOtherFreeModels(base, agg); len(out) != 1 || out[0].ID != "m1" {
		t.Fatalf("single-vendor must be no-op: %v", out)
	}
	if nilOut := appendOtherFreeModels(base, nil); len(nilOut) != 1 {
		t.Fatalf("nil aggregator must be no-op: %v", nilOut)
	}
}

// aggModelVendor 简化模型厂商（仅目录，实现 contract.Vendor）。
type aggModelVendor struct {
	id     string
	models []string
}

func (m *aggModelVendor) ID() string   { return m.id }
func (m *aggModelVendor) Name() string { return m.id }
func (m *aggModelVendor) ListModels(_ context.Context) ([]contract.Model, error) {
	out := make([]contract.Model, 0, len(m.models))
	for _, mm := range m.models {
		out = append(out, contract.Model{ID: mm, Provider: m.id, Free: true})
	}
	return out, nil
}
func (m *aggModelVendor) IsFree(_ string) bool { return true }
func (m *aggModelVendor) Chat(_ context.Context, _ *contract.Message) (*contract.Reply, error) {
	return nil, nil
}
func (m *aggModelVendor) ChatStream(_ context.Context, _ *contract.Message) (*contract.Stream, error) {
	return nil, nil
}
func (m *aggModelVendor) Auth(_ *http.Request) string { return "" }
func (m *aggModelVendor) ErrSemantics() contract.ErrRules {
	return contract.ErrRules{}
}
func (m *aggModelVendor) Health() contract.VendorHealth {
	return contract.VendorHealth{}
}
