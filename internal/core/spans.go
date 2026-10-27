package core

import (
	"context"
	"io"
	"sync"

	"github.com/townsendmerino/tack/internal/tools"
)

// lazySpans is a tools.Symbols that opens its index at its first use, so a session that never needs a span pays
// nothing for it (Options.OpenSpans: ken's index over a large workspace takes seconds to build). A failed open is kept:
// every later call returns the same error at once.
type lazySpans struct {
	root string
	open func(ctx context.Context, root string) (tools.Symbols, error)

	mu  sync.Mutex
	s   tools.Symbols
	err error
}

// get is the open index, opening it on the first call.
func (l *lazySpans) get(ctx context.Context) (tools.Symbols, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.s == nil && l.err == nil {
		l.s, l.err = l.open(ctx, l.root)
	}
	return l.s, l.err
}

// Close closes the index if it was opened and is an io.Closer.
func (l *lazySpans) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.s.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (l *lazySpans) Definition(ctx context.Context, name string) ([]tools.Def, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.Definition(ctx, name)
}

func (l *lazySpans) Outline(ctx context.Context, path string) ([]tools.Def, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.Outline(ctx, path)
}

func (l *lazySpans) Callers(ctx context.Context, name string) ([]tools.Site, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.Callers(ctx, name)
}

func (l *lazySpans) Callees(ctx context.Context, name string) ([]tools.Site, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.Callees(ctx, name)
}

func (l *lazySpans) TestsFor(ctx context.Context, name string) ([]tools.TestSite, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.TestsFor(ctx, name)
}

func (l *lazySpans) SymbolAt(ctx context.Context, path string, line int) ([]tools.Def, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.SymbolAt(ctx, path, line)
}

func (l *lazySpans) Sync(ctx context.Context, paths []string) (bool, error) {
	s, err := l.get(ctx)
	if err != nil {
		return false, err
	}
	return s.Sync(ctx, paths)
}

var _ tools.Symbols = (*lazySpans)(nil)
