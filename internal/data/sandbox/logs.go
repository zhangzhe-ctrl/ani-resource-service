package sandbox

import (
	"context"
	"errors"
	"io"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

const (
	sandboxLogFrameBytes   = 16 * 1024
	sandboxLogMaxBytes     = 8 * 1024 * 1024
	sandboxLogLifetime     = 10 * time.Minute
	sandboxLogOpenTimeout  = 15 * time.Second
	sandboxLogPollInterval = 2 * time.Second
)

type sandboxPodLogs interface {
	Open(context.Context, biz.Sandbox, biz.LogOptions) (io.ReadCloser, error)
}

type kubernetesSandboxPodLogs struct{ client coreclient.CoreV1Interface }

func (l kubernetesSandboxPodLogs) Open(ctx context.Context, s biz.Sandbox, opts biz.LogOptions) (io.ReadCloser, error) {
	tail := int64(opts.TailLines)
	request := &corev1.PodLogOptions{Container: "sandbox", Follow: opts.Follow, Timestamps: true}
	if opts.Since != nil {
		request.SinceTime = &metav1.Time{Time: opts.Since.UTC()}
	} else {
		request.TailLines = &tail
	}
	return l.client.Pods(s.Snapshot.Namespace).GetLogs(s.ProviderRefs.PodName, request).Stream(ctx)
}

// KubernetesSandboxLogs keeps the long-lived log client separate from the
// short-timeout dynamic client used for identity observations.
type KubernetesSandboxLogs struct {
	provider *KubernetesProvider
	reader   biz.DataPlaneReader
	source   sandboxPodLogs
}

func NewKubernetesSandboxLogs(config *rest.Config, provider *KubernetesProvider, reader biz.DataPlaneReader) (*KubernetesSandboxLogs, error) {
	if config == nil || provider == nil || reader == nil {
		return nil, biz.ErrInvalid
	}
	copyConfig := rest.CopyConfig(config)
	copyConfig.Timeout = 0
	client, err := coreclient.NewForConfig(copyConfig)
	if err != nil {
		return nil, err
	}
	return &KubernetesSandboxLogs{provider: provider, reader: reader, source: kubernetesSandboxPodLogs{client: client}}, nil
}

func sameLogSandbox(a, b biz.Sandbox) bool {
	return a.ID == b.ID && a.TenantID == b.TenantID && a.Version == b.Version &&
		b.Desired == biz.DesiredRunning && b.Observed == a.Observed && b.Pending == nil &&
		a.Snapshot.ClusterID == b.Snapshot.ClusterID && a.Snapshot.Namespace == b.Snapshot.Namespace &&
		a.Snapshot.NamespaceUID == b.Snapshot.NamespaceUID && a.ProviderRefs.PodName == b.ProviderRefs.PodName && a.ProviderRefs.PodUID == b.ProviderRefs.PodUID
}

// check performs observations on both sides of the Pod GET. These checks
// detect replacement and lifecycle changes but cannot make the Kubernetes and
// database reads atomic; the stream is also rechecked before every data frame.
func (l *KubernetesSandboxLogs) check(ctx context.Context, s biz.Sandbox) error {
	if err := biz.RequireTenant(ctx, s.TenantID); err != nil {
		return err
	}
	if l == nil || l.provider == nil || l.reader == nil || !biz.UUID(s.ID) || s.Version < 1 ||
		s.Desired != biz.DesiredRunning || (s.Observed != biz.ObservedRunning && s.Observed != biz.ObservedProvisioning && s.Observed != biz.ObservedUnknown) || s.Pending != nil ||
		s.ProviderRefs.PodName != "sandbox-"+s.ID || s.ProviderRefs.PodUID == "" {
		return biz.ErrDataPlaneUnavailable
	}
	if err := l.provider.requireCluster(s); err != nil {
		return err
	}
	current, err := l.reader.Get(ctx, s.TenantID, s.ID)
	if err != nil {
		return err
	}
	if !sameLogSandbox(s, current) {
		return biz.ErrDataPlaneUnavailable
	}
	uid, err := l.provider.verifyTenantNamespace(ctx, s, "")
	if err != nil {
		return err
	}
	pod, err := l.provider.client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Get(ctx, s.ProviderRefs.PodName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return biz.ErrDataPlaneUnavailable
	}
	if err != nil {
		return err
	}
	if string(pod.GetUID()) != s.ProviderRefs.PodUID || !owned(pod, s) || !l.provider.matchesPod(pod, s) || pod.GetDeletionTimestamp() != nil {
		return biz.ErrDataPlaneUnavailable
	}
	if _, err = l.provider.verifyTenantNamespace(ctx, s, uid); err != nil {
		return err
	}
	current, err = l.reader.Get(ctx, s.TenantID, s.ID)
	if err != nil {
		return err
	}
	if !sameLogSandbox(s, current) {
		return biz.ErrDataPlaneUnavailable
	}
	return nil
}

func (l *KubernetesSandboxLogs) StreamLogs(parent context.Context, s biz.Sandbox, opts biz.LogOptions, emit func(biz.LogFrame) error) error {
	if l == nil || l.source == nil || emit == nil || opts.TailLines < 1 || opts.TailLines > 1000 ||
		(opts.Since == nil && opts.PodUID != "") || (opts.Since != nil && (opts.Since.IsZero() || opts.PodUID == "")) {
		return biz.ErrInvalid
	}
	if opts.PodUID != "" && opts.PodUID != s.ProviderRefs.PodUID {
		return biz.ErrDataPlaneUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, sandboxLogLifetime)
	defer cancel()
	if err := l.check(ctx, s); err != nil {
		return err
	}
	opening := time.AfterFunc(sandboxLogOpenTimeout, cancel)
	body, err := l.source.Open(ctx, s, opts)
	stopped := opening.Stop()
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		if parent.Err() != nil {
			return parent.Err()
		}
		if !stopped {
			return biz.ErrDependencyUnavailable
		}
		return err
	}
	if body == nil {
		return biz.ErrDependencyUnavailable
	}
	defer body.Close()
	if !stopped || ctx.Err() != nil {
		return biz.ErrDependencyUnavailable
	}
	if err = l.check(ctx, s); err != nil {
		return err
	}
	if err = emit(biz.LogFrame{Kind: "READY", PodUID: s.ProviderRefs.PodUID}); err != nil {
		return err
	}
	type chunk struct {
		data []byte
		err  error
	}
	chunks := make(chan chunk, 4)
	go func() {
		for {
			buf := make([]byte, sandboxLogFrameBytes)
			n, e := body.Read(buf)
			if n > 0 {
				select {
				case chunks <- chunk{data: buf[:n]}:
				case <-ctx.Done():
					return
				}
			}
			if e != nil {
				select {
				case chunks <- chunk{err: e}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	ticker := time.NewTicker(sandboxLogPollInterval)
	defer ticker.Stop()
	remaining := sandboxLogMaxBytes
	for {
		select {
		case <-ctx.Done():
			if parent.Err() != nil {
				return parent.Err()
			}
			return emit(biz.LogFrame{Kind: "END", Reason: "TIME_LIMIT"})
		case <-ticker.C:
			if err := l.check(ctx, s); err != nil {
				return emit(biz.LogFrame{Kind: "END", Reason: "TARGET_UNAVAILABLE"})
			}
		case c := <-chunks:
			if len(c.data) > 0 {
				if err := l.check(ctx, s); err != nil {
					return emit(biz.LogFrame{Kind: "END", Reason: "TARGET_UNAVAILABLE"})
				}
				if len(c.data) > remaining {
					c.data = c.data[:remaining]
				}
				if err := emit(biz.LogFrame{Kind: "DATA", Data: c.data}); err != nil {
					return err
				}
				remaining -= len(c.data)
				if remaining == 0 {
					return emit(biz.LogFrame{Kind: "END", Reason: "BYTE_LIMIT"})
				}
			}
			if c.err != nil {
				if errors.Is(c.err, io.EOF) {
					return emit(biz.LogFrame{Kind: "END", Reason: "EOF"})
				}
				return emit(biz.LogFrame{Kind: "ERROR", Reason: "UPSTREAM_UNAVAILABLE"})
			}
		}
	}
}
