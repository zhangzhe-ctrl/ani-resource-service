package vm

import (
	"errors"
	"strings"
	"testing"
)

func TestListFilterDefaultsAndValidation(t *testing.T) {
	f, e := ListFilter{}.Validated()
	if e != nil || f.Page != 1 || f.PageSize != 20 || f.State != "" || f.Keyword != "" {
		t.Fatalf("defaults: %+v %v", f, e)
	}
	if _, e = (ListFilter{PageSize: 101}).Validated(); !errors.Is(e, ErrInvalid) {
		t.Fatalf("page size above the cap must be rejected: %v", e)
	}
	f, e = (ListFilter{Page: 3, PageSize: 50}).Validated()
	if e != nil || f.Page != 3 || f.PageSize != 50 {
		t.Fatalf("explicit window: %+v %v", f, e)
	}
}

func TestListFilterStateIsClosedAndNormalized(t *testing.T) {
	for _, state := range []string{"pending", "provisioning", "starting", "stopping", "running", "stopped", "failed", "deleting", "deleted"} {
		if _, e := (ListFilter{State: state}).Validated(); e != nil {
			t.Fatalf("state %s must be accepted: %v", state, e)
		}
	}
	f, e := (ListFilter{State: " all "}).Validated()
	if e != nil || f.State != "" {
		t.Fatalf("all must normalize to the default filter: %+v %v", f, e)
	}
	for _, state := range []string{"Running", "bogus", "delete"} {
		if _, e = (ListFilter{State: state}).Validated(); !errors.Is(e, ErrInvalid) {
			t.Fatalf("state %q must be rejected: %v", state, e)
		}
	}
}

func TestListFilterKeywordBounds(t *testing.T) {
	f, e := (ListFilter{Keyword: "  vm-linux  "}).Validated()
	if e != nil || f.Keyword != "vm-linux" {
		t.Fatalf("keyword must be trimmed: %q %v", f.Keyword, e)
	}
	if _, e = (ListFilter{Keyword: strings.Repeat("字", 81)}).Validated(); !errors.Is(e, ErrInvalid) {
		t.Fatalf("keyword above 80 runes must be rejected: %v", e)
	}
	if _, e = (ListFilter{Keyword: strings.Repeat("字", 80)}).Validated(); e != nil {
		t.Fatalf("80-rune keyword must be accepted: %v", e)
	}
}
