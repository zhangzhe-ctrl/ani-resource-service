package biz

import (
	"strings"
	"testing"
	"time"
)

func TestCursorTenantScopeFilterAndSignature(t *testing.T) {
	codec, err := NewCursorCodec([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	tenant := "11111111-1111-4111-8111-111111111111"
	other := "22222222-2222-4222-8222-222222222222"
	f := Filter{Search: "100%", Purposes: []string{"training", "container"}, Accelerator: "nvidia", Limit: 2}
	key := PageKey{CreatedAt: time.Now().UTC(), ImageID: "img_" + strings.Repeat("a", 32)}
	cursor, err := codec.EncodeCursor(tenant, TenantImages, f, key)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := codec.DecodeCursor(cursor, tenant, TenantImages, f)
	if err != nil || actual.ImageID != key.ImageID || !actual.CreatedAt.Equal(key.CreatedAt) {
		t.Fatal("cursor roundtrip", err)
	}
	reordered := f
	reordered.Purposes = []string{"container", "training"}
	if _, err = codec.DecodeCursor(cursor, tenant, TenantImages, reordered); err != nil {
		t.Fatal("equivalent normalized filters rejected")
	}
	for _, bad := range []string{"!", cursor + "x", cursor[:len(cursor)-10], strings.Repeat("a", 4097), "x.y.z"} {
		if _, err = codec.DecodeCursor(bad, tenant, TenantImages, f); ReasonOf(err) != InvalidCursor {
			t.Fatal("invalid signature accepted", err)
		}
	}
	if _, err = codec.DecodeCursor(cursor, other, TenantImages, f); ReasonOf(err) != InvalidCursor {
		t.Fatal("cross tenant cursor", err)
	}
	if _, err = codec.DecodeCursor(cursor, tenant, PlatformImages, f); ReasonOf(err) != InvalidCursor {
		t.Fatal("cross scope cursor", err)
	}
	for _, mutate := range []func(*Filter){func(v *Filter) { v.Search = "100_" }, func(v *Filter) { v.Limit = 3 }, func(v *Filter) { v.Purposes = []string{"container"} }, func(v *Filter) { v.Accelerator = "amd" }} {
		changed := f
		mutate(&changed)
		if _, err = codec.DecodeCursor(cursor, tenant, TenantImages, changed); ReasonOf(err) != InvalidCursor {
			t.Fatal("filter cursor", err)
		}
	}
	if _, err = NewCursorCodec([]byte("short")); err == nil {
		t.Fatal("weak cursor key accepted")
	}
}
