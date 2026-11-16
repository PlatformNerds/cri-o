package server

import (
	"testing"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	types "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/cri-o/cri-o/internal/storage"
)

func TestAddImageOSMounts(t *testing.T) {
	mounts := func(img *storage.ImageResult, cc *types.ContainerConfig, pre ...rspec.Mount) map[string]string {
		g, err := generate.New("freebsd")
		if err != nil {
			t.Fatal(err)
		}
		g.ClearMounts()
		for _, m := range pre {
			g.AddMount(m)
		}
		addImageOSMounts(&g, img, cc)
		got := map[string]string{}
		for _, m := range g.Mounts() {
			got[m.Destination] = m.Type
		}
		return got
	}
	linux := &storage.ImageResult{OCIConfig: &specs.Image{Platform: specs.Platform{OS: "linux"}}}
	freebsd := &storage.ImageResult{OCIConfig: &specs.Image{Platform: specs.Platform{OS: "freebsd"}}}

	if got := mounts(freebsd, &types.ContainerConfig{}); len(got) != 0 {
		t.Errorf("FreeBSD image got Linux mounts: %v", got)
	}
	got := mounts(linux, &types.ContainerConfig{})
	if got["/proc"] != "linprocfs" || got["/sys"] != "linsysfs" || got["/dev/shm"] != "tmpfs" {
		t.Errorf("Linux image mounts: %v", got)
	}
	// a /dev/shm the pod supplies (emptyDir) and an existing /proc win
	got = mounts(linux, &types.ContainerConfig{Mounts: []*types.Mount{{ContainerPath: "/dev/shm"}}},
		rspec.Mount{Destination: "/proc", Type: "procfs"})
	if got["/proc"] != "procfs" || got["/dev/shm"] != "" || got["/sys"] != "linsysfs" {
		t.Errorf("supplied mounts not respected: %v", got)
	}
}
