package timing

import (
	"context"
	"testing"
	"time"
)

func TestSleepOrWakesOnChannel(t *testing.T) {
	ctx := context.Background()
	wake := make(chan struct{}, 1)
	wake <- struct{}{}

	start := time.Now()
	SleepOr(ctx, wake, time.Second)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("SleepOr did not return on wake: %v", time.Since(start))
	}
}

func TestSleepOrReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	SleepOr(ctx, nil, time.Second)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("SleepOr did not return on cancel: %v", time.Since(start))
	}
}
