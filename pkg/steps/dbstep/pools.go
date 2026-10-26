package dbstep

import (
	"context"
	"errors"
	"sync"
)

// Pools is a run's open databases: one Conn per connection URL, opened by the
// first step that asks for it and shared by every step after, in every
// scenario and every --jobs worker. The runner makes one per run and closes it
// when the run ends; a step borrows and never closes.
type Pools struct {
	mu    sync.Mutex
	conns map[string]*pooled
}

// pooled is one URL's Conn, or the error opening it gave. ready is closed once
// either is set, so a second step asking while the first is still connecting
// waits for that connection rather than opening a second.
type pooled struct {
	ready chan struct{}
	conn  Conn
	err   error
}

// NewPools returns an empty set: nothing is opened until a step asks.
func NewPools() *Pools {
	return &Pools{conns: map[string]*pooled{}}
}

// Conn returns the open connection for rawURL, opening it on the first ask.
//
// A connection that could not be opened is not kept: the next ask -- a retry,
// a later step -- dials again, because a database that was still starting is
// the common reason and remembering the failure would fail every step after it.
func (p *Pools) Conn(ctx context.Context, rawURL string) (Conn, error) {
	p.mu.Lock()
	if e, ok := p.conns[rawURL]; ok {
		p.mu.Unlock()
		select {
		case <-e.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if e.err == nil {
			return e.conn, nil
		}
		return nil, e.err
	}
	e := &pooled{ready: make(chan struct{})}
	p.conns[rawURL] = e
	p.mu.Unlock()

	e.conn, e.err = open(ctx, rawURL)
	if e.err != nil {
		p.mu.Lock()
		delete(p.conns, rawURL)
		p.mu.Unlock()
	}
	close(e.ready)
	return e.conn, e.err
}

// Close closes every connection the run opened. Calling it twice is safe.
func (p *Pools) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	conns := p.conns
	p.conns = map[string]*pooled{}
	p.mu.Unlock()
	var errs []error
	for _, e := range conns {
		<-e.ready
		if e.conn != nil {
			errs = append(errs, e.conn.Close())
		}
	}
	return errors.Join(errs...)
}

// open connects to rawURL through the driver its scheme names.
func open(ctx context.Context, rawURL string) (Conn, error) {
	d, err := driverFor(rawURL)
	if err != nil {
		return nil, err
	}
	return d.Open(ctx, rawURL)
}

type poolsKey struct{}

// WithPools returns a copy of ctx carrying p.
func WithPools(ctx context.Context, p *Pools) context.Context {
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, poolsKey{}, p)
}

// PoolsOf returns the Pools on ctx and whether there was one.
func PoolsOf(ctx context.Context) (*Pools, bool) {
	if ctx == nil {
		return nil, false
	}
	p, ok := ctx.Value(poolsKey{}).(*Pools)
	return p, ok && p != nil
}
