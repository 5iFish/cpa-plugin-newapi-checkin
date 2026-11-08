package abi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"sync"
)

type HostCaller interface {
	Call(context.Context, string, any, any) error
}
type Bridge struct {
	mu     sync.Mutex
	closed bool
	invoke func(string, []byte) ([]byte, error)
}

func NewBridge(invoke func(string, []byte) ([]byte, error)) *Bridge { return &Bridge{invoke: invoke} }
func (b *Bridge) Call(ctx context.Context, method string, request, response any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("host bridge closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	raw, err = b.invoke(method, raw)
	if err != nil {
		return errors.New("host callback failed")
	}
	var e pluginabi.Envelope
	if json.Unmarshal(raw, &e) != nil || !e.OK {
		return errors.New("host callback rejected")
	}
	if response == nil {
		return nil
	}
	return json.Unmarshal(e.Result, response)
}
func (b *Bridge) Close() { b.mu.Lock(); defer b.mu.Unlock(); b.closed = true }
