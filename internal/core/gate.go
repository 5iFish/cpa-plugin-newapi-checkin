package core

import (
	"context"
	"sync"
)

// Gate serializes mutations. A callback must never enter the same gate again.
type Gate struct {
	once sync.Once
	slot chan struct{}
}

func (g *Gate) Do(ctx context.Context, fn func() error) error {
	g.once.Do(func() { g.slot = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case g.slot <- struct{}{}:
	}
	defer func() { <-g.slot }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
