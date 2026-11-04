package sandbox

import (
	"context"
	"net/http/httptrace"
	"sync/atomic"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

// client-go may retry a mutating request on Retry-After; its final rejection
// cannot rule out an earlier unknown attempt. Require one complete HTTP write
// before treating a negative API status as a definitive rejection.
type checkpointWriteTrace struct {
	attempts atomic.Int32
	writes   atomic.Int32
	failed   atomic.Bool
}

func (t *checkpointWriteTrace) context(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { t.attempts.Add(1) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				t.failed.Store(true)
			}
			t.writes.Add(1)
		},
	})
}
func (t *checkpointWriteTrace) singleComplete() bool {
	return t.attempts.Load() == 1 && t.writes.Load() == 1 && !t.failed.Load()
}
func (t *checkpointWriteTrace) outcome(err error) string {
	outcome := mutationOutcome(err)
	if outcome == biz.MutationRejected && !t.singleComplete() {
		return biz.MutationUnknown
	}
	return outcome
}
