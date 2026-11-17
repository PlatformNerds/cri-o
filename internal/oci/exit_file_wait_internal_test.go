package oci

import (
	"context"
	"testing"
)

func TestExitFileWaitSkipped(t *testing.T) {
	t.Parallel()

	if exitFileWaitSkipped(context.Background()) {
		t.Fatal("plain context must not skip the exit file wait")
	}

	if !exitFileWaitSkipped(WithoutExitFileWait(context.Background())) {
		t.Fatal("WithoutExitFileWait context must skip the exit file wait")
	}

	type otherKey struct{}

	derived := context.WithValue(WithoutExitFileWait(context.Background()), otherKey{}, 1)
	if !exitFileWaitSkipped(derived) {
		t.Fatal("derived context must keep skipping the exit file wait")
	}
}
