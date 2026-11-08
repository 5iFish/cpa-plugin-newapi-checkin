package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCanceledGateWait(t *testing.T) {
	g := &Gate{}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = g.Do(context.Background(), func() error { close(started); <-release; return nil })
	}()
	<-started
	ctx, c := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer c()
	if !errors.Is(g.Do(ctx, func() error { t.Error("canceled callback ran"); return nil }), context.DeadlineExceeded) {
		t.Fatal("wait not canceled")
	}
	close(release)
	<-done
}
