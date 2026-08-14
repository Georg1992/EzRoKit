package autopot

import (
	"context"
	"testing"

	"belarus-champ-tools/runner/autopot/statusui"
)

// TestStatusUIReaderCancel verifies that ReadValues returns an error
// (context.Canceled) when called with an already-cancelled context.
func TestStatusUIReaderUninitialized(t *testing.T) {
	var nilReader *statusUIReader
	if result := nilReader.ReadValues(context.Background()); result.Err == nil {
		t.Fatal("nil statusUIReader returned no error")
	}
	if result := (&statusUIReader{}).ReadValues(context.Background()); result.Err == nil {
		t.Fatal("statusUIReader without poller returned no error")
	}
}

func TestStatusUIReaderCancel(t *testing.T) {
	pipeline, err := statusui.NewDefaultPipeline()
	if err != nil {
		t.Skipf("skipping: cannot create pipeline in test env: %v", err)
	}

	reader := &statusUIReader{
		poller: statusui.NewStripPoller(pipeline),
		log:    func(string) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := reader.ReadValues(ctx)
	if result.Err == nil {
		t.Fatal("ReadValues with cancelled ctx: want error, got nil")
	}
}
