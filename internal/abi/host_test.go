package abi

import (
	"context"
	"errors"
	"testing"
)

func TestBridgeShutdownStopsCallbacks(t *testing.T) {
	n := 0
	b := NewBridge(func(string, []byte) ([]byte, error) { n++; return []byte(`{"ok":true,"result":{"value":1}}`), nil })
	var v map[string]int
	if err := b.Call(context.Background(), "host.auth.list", map[string]any{}, &v); err != nil || v["value"] != 1 {
		t.Fatal(err, v)
	}
	b.Close()
	if b.Call(context.Background(), "host.auth.list", nil, nil) == nil || n != 1 {
		t.Fatal("callback after shutdown")
	}
}
func TestCanceledBridgeDoesNotCallHost(t *testing.T) {
	ctx, c := context.WithCancel(context.Background())
	c()
	b := NewBridge(func(string, []byte) ([]byte, error) { t.Fatal("callback after cancel"); return nil, nil })
	if !errors.Is(b.Call(ctx, "host.auth.list", nil, nil), context.Canceled) {
		t.Fatal("not canceled")
	}
}
