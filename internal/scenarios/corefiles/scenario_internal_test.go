package corefiles

import (
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/scenarios"
)

// TestCNEInstanceRef pins the CNEInstance the scenario patches to what Phase
// 22 actually applies (f5-cne-system/<cluster>-bnk). The previous hardcoded
// default/bnk-instance never existed on an gkebnkctl cluster, so the patch was
// a silent no-op on every example.
func TestCNEInstanceRef(t *testing.T) {
	sctx := &scenarios.Context{
		Cluster: &intent.Cluster{Metadata: intent.Metadata{Name: "bnk-extonly"}},
		Options: map[string]string{},
	}
	ns, name := cneInstanceRef(sctx)
	if ns != "f5-cne-system" || name != "bnk-extonly-bnk" {
		t.Errorf("cneInstanceRef = %s/%s, want f5-cne-system/bnk-extonly-bnk", ns, name)
	}

	sctx.Options["cne-namespace"] = "alpha"
	sctx.Options["cne-instance"] = "bnk-instance"
	ns, name = cneInstanceRef(sctx)
	if ns != "alpha" || name != "bnk-instance" {
		t.Errorf("cneInstanceRef with overrides = %s/%s, want alpha/bnk-instance", ns, name)
	}
}
