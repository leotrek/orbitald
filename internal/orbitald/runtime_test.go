package orbitald

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapSnapshotterErrorAddsPrivilegeHint(t *testing.T) {
	err := wrapSnapshotterError("unpack image", "overlayfs", errors.New("operation not permitted"))
	if !strings.Contains(err.Error(), `unpack image with snapshotter "overlayfs"`) {
		t.Fatalf("missing snapshotter context: %v", err)
	}
	if !strings.Contains(err.Error(), "needs mount privileges") {
		t.Fatalf("missing privilege hint: %v", err)
	}
}
