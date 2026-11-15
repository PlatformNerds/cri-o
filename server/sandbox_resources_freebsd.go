package server

import (
	"context"
	"sync"

	"github.com/opencontainers/runtime-tools/generate"
	"golang.org/x/sys/unix"
	types "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/cri-o/cri-o/internal/log"
)

// podLevelLimitsSupported reports whether rctl enforces a jail's rules on its
// child jails (kern.features.rctl_jail_hierarchy). Without that, a rule on
// the pod jail would count the containers' usage but only apply to the pod
// jail's own process (the infra container), so memoryuse:sigkill would kill
// the infra container when the containers used too much.
var podLevelLimitsSupported = sync.OnceValue(func() bool {
	v, err := unix.SysctlUint32("kern.features.rctl_jail_hierarchy")
	return err == nil && v == 1
})

// applyPodLevelLimits puts the pod's limits on the infra container's spec.
//
// On FreeBSD there are no pod cgroups: the infra container's jail is the pod
// jail, the parent of every container jail in the pod, and the OCI runtime
// (ocijail) turns linux.resources into rctl(8) rules on it. Since rctl
// accounts child jails to their parent (and, with the FreeBSD fix "rctl:
// enforce jail rules on processes in child jails", enforces parent rules on
// them), these limit the pod as a whole, like the pod cgroup on Linux.
//
// The kubelet computes them like the pod cgroup's: memory only if every
// container declares a memory limit, CPU quota only if every container
// declares a CPU limit, and pod overhead is already included.
func applyPodLevelLimits(ctx context.Context, g *generate.Generator, resources *types.LinuxContainerResources, supported bool) {
	if resources == nil || (resources.GetMemoryLimitInBytes() <= 0 && resources.GetCpuQuota() <= 0) {
		return
	}
	if !supported {
		log.Infof(ctx, "Not enforcing pod-level limits: the kernel doesn't apply jail rctl rules to child jails (kern.features.rctl_jail_hierarchy)")
		return
	}
	if mem := resources.GetMemoryLimitInBytes(); mem > 0 {
		g.SetLinuxResourcesMemoryLimit(mem)
	}
	if quota, period := resources.GetCpuQuota(), resources.GetCpuPeriod(); quota > 0 && period > 0 {
		g.SetLinuxResourcesCPUQuota(quota)
		g.SetLinuxResourcesCPUPeriod(uint64(period))
	}
}
