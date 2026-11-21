package container

import (
	types "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/cri-o/cri-o/internal/config/nsmgr"
	"github.com/cri-o/cri-o/internal/jailparam"
	oci "github.com/cri-o/cri-o/internal/oci"
	"github.com/cri-o/cri-o/pkg/config"
)

func (c *container) SpecAddNamespaces(sb SandboxIFace, targetCtr *oci.Container, serverConfig *config.Config) error {
	// Join the namespace paths for the pod sandbox container.
	managedNamespaces := sb.NamespacePaths()

	for _, ns := range managedNamespaces {
		if ns.Type() == nsmgr.NETNS {
			c.Spec().AddAnnotation("org.freebsd.parentJail", ns.Path())
		}
	}

	// Privileged containers of a host-network pod may manage the host pf;
	// the sandbox jail got the same permission (see RunPodSandbox).
	hostNetwork := sb.NamespaceOptions().GetNetwork() == types.NamespaceMode_NODE
	if jailparam.HostNetworkPFAllowed(hostNetwork, c.Privileged()) {
		c.Spec().AddAnnotation(jailparam.AllowPFAnnotation, "true")
	}
	// Privileged containers of a pod with its own network may serve NFS;
	// the sandbox jail got the same permission.
	if jailparam.PodNFSDAllowed(hostNetwork, c.Privileged()) {
		c.Spec().AddAnnotation(jailparam.AllowNFSDAnnotation, "true")
	}

	return nil
}
