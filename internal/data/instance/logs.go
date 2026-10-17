package instance

import (
	"context"
	"errors"
	"io"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

type podLogSource interface {
	Open(context.Context, string, string, uint32, bool) (io.ReadCloser, error)
}
type kubernetesPodLogs struct{ client coreclient.CoreV1Interface }

func (s kubernetesPodLogs) Open(ctx context.Context, namespace, pod string, tail uint32, follow bool) (io.ReadCloser, error) {
	lines := int64(tail)
	return s.client.Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{Container: biz.Container, TailLines: &lines, Follow: follow}).Stream(ctx)
}

// ConfigureLogs creates a separate client with no whole-response HTTP timeout.
// Opening is bounded separately; cancellation and lifecycle bound the body.
func (n *Notebook) ConfigureLogs(config *rest.Config, limits biz.LogLimits) error {
	if config == nil || biz.ValidateLogLimits(limits) != nil {
		return biz.ErrInvalid
	}
	c := rest.CopyConfig(config)
	c.Timeout = 0
	client, e := coreclient.NewForConfig(c)
	if e != nil {
		return e
	}
	n.logSource = kubernetesPodLogs{client: client}
	n.logLimits = limits.Defaults()
	return nil
}
func (n *Notebook) checkLogPod(ctx context.Context, i biz.Instance, target runtimeTarget) error {
	if target.pod == nil || target.statefulSet == nil {
		return biz.ErrDiagnosticTarget
	}
	p, e := n.client.Resource(podGVR).Namespace(i.Spec.Namespace).Get(ctx, target.pod.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		return biz.ErrPodChanged
	}
	if e != nil {
		return e
	}
	if p.GetUID() != target.pod.GetUID() || p.GetDeletionTimestamp() != nil || !owned(p, i, "") || !controlledBy(p, "apps", "StatefulSet", i.Spec.NotebookName, string(target.statefulSet.GetUID())) {
		return biz.ErrPodChanged
	}
	if e = verifyPod(p, i); e != nil {
		return biz.ErrOwnership
	}
	return nil
}
func (n *Notebook) StreamLogs(ctx context.Context, i biz.Instance, tail uint32, follow bool, emit func(biz.LogFrame) error) error {
	if n.logSource == nil || tail < 1 || tail > 1000 || emit == nil {
		return biz.ErrDiagnosticTarget
	}
	target, e := n.resolveRuntimeTarget(ctx, i)
	if e != nil {
		return e
	}
	if target.pod == nil || target.pod.GetDeletionTimestamp() != nil {
		return biz.ErrDiagnosticTarget
	}
	if e = n.checkLogPod(ctx, i, target); e != nil {
		return e
	}
	limits := n.logLimits.Defaults()
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	opening := time.AfterFunc(time.Duration(limits.OpenTimeoutSeconds)*time.Second, cancel)
	body, e := n.logSource.Open(streamCtx, i.Spec.Namespace, target.pod.GetName(), tail, follow)
	stopped := opening.Stop()
	if e != nil {
		if body != nil {
			_ = body.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !stopped {
			return biz.ErrDiagnosticTarget
		}
		return e
	}
	if body == nil {
		return biz.ErrDiagnosticTarget
	}
	defer body.Close()
	if !stopped || streamCtx.Err() != nil {
		return biz.ErrDiagnosticTarget
	}
	// No body bytes are released until this second GET confirms the same UID.
	if e = n.checkLogPod(ctx, i, target); e != nil {
		return e
	}
	if e = emit(biz.LogFrame{Kind: "READY"}); e != nil {
		return e
	}
	frameBytes := limits.FrameBytes
	if frameBytes > limits.BufferBytes/2 {
		frameBytes = limits.BufferBytes / 2
	}
	if frameBytes < 1 {
		return biz.ErrInvalid
	}
	capacity := limits.BufferBytes/frameBytes - 2
	if capacity > 8 {
		capacity = 8
	}
	if capacity < 1 {
		capacity = 1
	}
	type chunk struct {
		data []byte
		err  error
	}
	chunks := make(chan chunk, capacity)
	go func() {
		for {
			buffer := make([]byte, frameBytes)
			n, e := body.Read(buffer)
			if n > 0 {
				select {
				case chunks <- chunk{data: buffer[:n]}:
				case <-streamCtx.Done():
					return
				}
			}
			if e != nil {
				select {
				case chunks <- chunk{err: e}:
				case <-streamCtx.Done():
				}
				return
			}
		}
	}()
	ticker := time.NewTicker(time.Duration(limits.TargetPollSeconds) * time.Second)
	defer ticker.Stop()
	for {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return emit(biz.LogFrame{Kind: "END", Reason: "TIME_LIMIT"})
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return emit(biz.LogFrame{Kind: "END", Reason: "TIME_LIMIT"})
			}
			return ctx.Err()
		case <-ticker.C:
			if e = n.checkLogPod(ctx, i, target); e != nil {
				if errors.Is(e, biz.ErrPodChanged) {
					return emit(biz.LogFrame{Kind: "END", Reason: "POD_CHANGED"})
				}
				return emit(biz.LogFrame{Kind: "ERROR", Reason: "INSTANCE_LOG_TARGET_UNAVAILABLE"})
			}
		case c := <-chunks:
			if len(c.data) > 0 {
				if e = emit(biz.LogFrame{Kind: "DATA", Data: c.data}); e != nil {
					return e
				}
			}
			if c.err != nil {
				if errors.Is(c.err, io.EOF) {
					return emit(biz.LogFrame{Kind: "END", Reason: "EOF"})
				}
				return emit(biz.LogFrame{Kind: "ERROR", Reason: "INSTANCE_LOG_UPSTREAM_UNAVAILABLE"})
			}
		}
	}
}
