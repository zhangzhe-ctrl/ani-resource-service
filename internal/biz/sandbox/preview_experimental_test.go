package sandbox

import (
	"errors"
	"testing"
	"time"
)

func TestExperimentalPreviewTransitions(t *testing.T) {
	now := time.Now().UTC()
	base := PreviewExperiment{Enabled: true, TenantID: "11111111-1111-4111-8111-111111111111", SandboxID: "22222222-2222-4222-8222-222222222222", PodUID: "pod-uid", Version: 1, Port: 8080, Protocol: "http", Desired: "open", ExpiresAt: now.Add(time.Hour)}
	t.Run("disabled refuses new open", func(t *testing.T) {
		p := base
		p.Enabled = false
		if _, err := DecideExperimentalPreview(p, PreviewExperimentObservation{}, true, now, "m1"); !errors.Is(err, ErrUnsupported) {
			t.Fatal(err)
		}
	})
	t.Run("persist intent before create", func(t *testing.T) {
		d, err := DecideExperimentalPreview(base, PreviewExperimentObservation{}, true, now, "m1")
		if err != nil || d.State != "opening" || d.Begin == nil || d.Begin.Kind != "create" {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("unknown create never retries", func(t *testing.T) {
		p := base
		p.Pending = &PreviewExperimentMutation{ID: "m1", Kind: "create"}
		for _, running := range []bool{true, false} {
			d, err := DecideExperimentalPreview(p, PreviewExperimentObservation{}, running, now, "m2")
			if err != nil || d.State != "unknown" || d.Begin != nil || d.ClearPending {
				t.Fatalf("%+v %v", d, err)
			}
		}
	})
	t.Run("attributed delayed create is recorded before cleanup", func(t *testing.T) {
		p := base
		p.Pending = &PreviewExperimentMutation{ID: "m1", Kind: "create"}
		d, err := DecideExperimentalPreview(p, PreviewExperimentObservation{Present: true, Attributable: true, ServiceUID: "svc-uid"}, false, now, "m2")
		if err != nil || d.Desired != "closed" || !d.ClearPending || d.ServiceUID != "svc-uid" || d.Begin != nil {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("pause and expiry start UID delete", func(t *testing.T) {
		p := base
		p.ServiceUID = "svc-uid"
		o := PreviewExperimentObservation{Present: true, Attributable: true, ServiceUID: "svc-uid", EndpointReady: true}
		for _, running := range []bool{false, true} {
			when := now
			if running {
				when = p.ExpiresAt
			}
			d, err := DecideExperimentalPreview(p, o, running, when, "m2")
			if err != nil || d.State != "closing" || d.Begin == nil || d.Begin.Kind != "delete" || d.Begin.TargetUID != "svc-uid" {
				t.Fatalf("%+v %v", d, err)
			}
		}
	})
	t.Run("disabled still cleans existing port", func(t *testing.T) {
		p := base
		p.Enabled = false
		p.ServiceUID = "svc-uid"
		d, err := DecideExperimentalPreview(p, PreviewExperimentObservation{Present: true, Attributable: true, ServiceUID: "svc-uid"}, true, now, "m2")
		if err != nil || d.Begin == nil || d.Begin.Kind != "delete" {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("delete observes absence before close", func(t *testing.T) {
		p := base
		p.Desired = "closed"
		p.ServiceUID = "svc-uid"
		p.Pending = &PreviewExperimentMutation{ID: "m2", Kind: "delete", TargetUID: "svc-uid"}
		blocked, err := DecideExperimentalPreview(p, PreviewExperimentObservation{}, true, now, "m3")
		if err != nil || blocked.ClearPending || blocked.State != "unknown" {
			t.Fatalf("unverified endpoint revocation: %+v %v", blocked, err)
		}
		d, err := DecideExperimentalPreview(p, PreviewExperimentObservation{AllSlicesAbsent: true}, true, now, "m3")
		if err != nil || !d.ClearPending || d.State != "closed" || d.ServiceUID != "" {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("reject replacement service", func(t *testing.T) {
		p := base
		p.ServiceUID = "svc-uid"
		_, err := DecideExperimentalPreview(p, PreviewExperimentObservation{Present: true, Attributable: true, ServiceUID: "replacement"}, true, now, "m3")
		if !errors.Is(err, ErrOwnership) {
			t.Fatal(err)
		}
	})
	t.Run("never declare missing endpoint ready", func(t *testing.T) {
		p := base
		p.ServiceUID = "svc-uid"
		d, err := DecideExperimentalPreview(p, PreviewExperimentObservation{Present: true, Attributable: true, ServiceUID: "svc-uid"}, true, now, "m3")
		if err != nil || d.State != "opening" {
			t.Fatalf("%+v %v", d, err)
		}
	})
}
