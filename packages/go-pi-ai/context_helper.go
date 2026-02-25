package ai

import "context"

// contextFromAbortSignal creates a context.Context that cancels when the abort signal fires.
func contextFromAbortSignal(signal *AbortSignal) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-signal.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}
