package server

import (
	"testing"

	"github.com/opencontainers/runtime-tools/generate"
	types "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestApplyPodLevelLimits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		res       *types.LinuxContainerResources
		wantMem   int64
		wantQuota int64
	}{
		{"none", nil, 0, 0},
		{"memory and cpu", &types.LinuxContainerResources{MemoryLimitInBytes: 256 << 20, CpuQuota: 50000, CpuPeriod: 100000}, 256 << 20, 50000},
		{"memory only (some container without a cpu limit)", &types.LinuxContainerResources{MemoryLimitInBytes: 128 << 20, CpuShares: 512}, 128 << 20, 0},
		{"quota without period ignored", &types.LinuxContainerResources{CpuQuota: 50000}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, err := generate.New("freebsd")
			if err != nil {
				t.Fatal(err)
			}
			applyPodLevelLimits(t.Context(), &g, tc.res, true)
			var mem, quota int64
			if r := g.Config.Linux; r != nil && r.Resources != nil {
				if r.Resources.Memory != nil && r.Resources.Memory.Limit != nil {
					mem = *r.Resources.Memory.Limit
				}
				if r.Resources.CPU != nil && r.Resources.CPU.Quota != nil {
					quota = *r.Resources.CPU.Quota
				}
			}
			if mem != tc.wantMem || quota != tc.wantQuota {
				t.Errorf("memory %d quota %d, want %d %d", mem, quota, tc.wantMem, tc.wantQuota)
			}
		})
	}
}

// Without hierarchical enforcement in the kernel, pod-level limits are not
// applied at all (they would hit the infra container instead of the pod).
func TestApplyPodLevelLimitsUnsupported(t *testing.T) {
	g, err := generate.New("freebsd")
	if err != nil {
		t.Fatal(err)
	}
	applyPodLevelLimits(t.Context(), &g, &types.LinuxContainerResources{MemoryLimitInBytes: 256 << 20}, false)
	if r := g.Config.Linux; r != nil && r.Resources != nil && r.Resources.Memory != nil && r.Resources.Memory.Limit != nil {
		t.Errorf("memory limit %d applied without kernel support", *r.Resources.Memory.Limit)
	}
}
