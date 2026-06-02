package tools

import "context"

// OutputFunc receives a chunk of a tool's live output as it is produced. The
// slice is only valid for the duration of the call — copy it to retain.
type OutputFunc func(chunk []byte)

type sinkKey struct{}

// WithOutputSink returns a context carrying sink, which streaming tools (only
// the shell today) call with their output as it is produced. Tools that don't
// stream ignore it, so the Tool interface stays unchanged.
func WithOutputSink(ctx context.Context, sink OutputFunc) context.Context {
	return context.WithValue(ctx, sinkKey{}, sink)
}

// outputSink returns the sink installed by WithOutputSink, or nil when none is
// set (e.g. patrol's connectionless path).
func outputSink(ctx context.Context) OutputFunc {
	s, _ := ctx.Value(sinkKey{}).(OutputFunc)
	return s
}
