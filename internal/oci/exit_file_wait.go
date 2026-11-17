package oci

import "context"

type skipExitFileWaitKey struct{}

// WithoutExitFileWait returns a context under which UpdateContainerStatus
// does not wait for the exit file of a container the runtime no longer
// knows about.
//
// The wait exists for a container that is exiting right now: its monitor may
// not have written the exit file yet. After a node reboot nothing survived to
// write one, so waiting only delays startup by the full backoff (about five
// seconds) per container, before the kubelet can connect.
func WithoutExitFileWait(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipExitFileWaitKey{}, true)
}

func exitFileWaitSkipped(ctx context.Context) bool {
	skip, ok := ctx.Value(skipExitFileWaitKey{}).(bool)

	return ok && skip
}
