package sandbox

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestFileRequestLimitsAndPaths(t *testing.T) {
	for _, path := range []string{"../bad", "/absolute", "a/../b", "a//b", "a\\b", ""} {
		if err := ValidateFileRequest(FileRequest{Action: "write", Path: path}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("path %q: %v", path, err)
		}
	}
	if err := ValidateFileRequest(FileRequest{Action: "write", Path: "ok", ContentBase64: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", SandboxFileMaxBytes)))}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFileRequest(FileRequest{Action: "write", Path: "ok", ContentBase64: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", SandboxFileMaxBytes+1)))}); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
	if err := ValidateFileRequest(FileRequest{Action: "list", Limit: 501}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit: %v", err)
	}
}

func TestFileListPaginationValidation(t *testing.T) {
	tests := []struct {
		name    string
		request FileRequest
		invalid bool
	}{
		{"defaults", FileRequest{Action: "list"}, false},
		{"page and size", FileRequest{Action: "list", Page: 2, PageSize: 100}, false},
		{"page overflow safe", FileRequest{Action: "list", Page: math.MaxUint32, PageSize: 100}, false},
		{"page size too large", FileRequest{Action: "list", PageSize: 101}, true},
		{"legacy", FileRequest{Action: "list", Limit: 500, Cursor: "10"}, false},
		{"legacy too large", FileRequest{Action: "list", Limit: 501}, true},
		{"mixed page and limit", FileRequest{Action: "list", Page: 1, Limit: 10}, true},
		{"mixed size and cursor", FileRequest{Action: "list", PageSize: 10, Cursor: "0"}, true},
		{"bad cursor", FileRequest{Action: "list", Cursor: "01"}, true},
		{"read with pagination", FileRequest{Action: "read", Path: "file", Page: 1}, true},
		{"write with pagination", FileRequest{Action: "write", Path: "file", PageSize: 1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFileRequest(tt.request)
			if errors.Is(err, ErrInvalid) != tt.invalid || (err != nil && !tt.invalid) {
				t.Fatalf("ValidateFileRequest() = %v; invalid = %v", err, tt.invalid)
			}
		})
	}
}

func TestFileListPagination(t *testing.T) {
	entries := []FileEntry{{Path: "z"}, {Path: "a"}, {Path: "c"}, {Path: "b"}}
	tests := []struct {
		name       string
		request    FileRequest
		paths      []string
		nextCursor string
	}{
		{"legacy defaults sorted", FileRequest{Action: "list"}, []string{"a", "b", "c", "z"}, ""},
		{"first page", FileRequest{Action: "list", Page: 1, PageSize: 2}, []string{"a", "b"}, ""},
		{"second page", FileRequest{Action: "list", Page: 2, PageSize: 2}, []string{"c", "z"}, ""},
		{"past end", FileRequest{Action: "list", Page: 3, PageSize: 2}, []string{}, ""},
		{"maximum page", FileRequest{Action: "list", Page: math.MaxUint32, PageSize: 100}, []string{}, ""},
		{"legacy first", FileRequest{Action: "list", Limit: 2}, []string{"a", "b"}, "2"},
		{"legacy next", FileRequest{Action: "list", Limit: 2, Cursor: "2"}, []string{"c", "z"}, ""},
		{"legacy past end", FileRequest{Action: "list", Cursor: "9999"}, []string{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := paginateFileResult(FileResult{Items: append([]FileEntry(nil), entries...)}, tt.request)
			paths := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				paths = append(paths, item.Path)
			}
			if !reflect.DeepEqual(paths, tt.paths) || result.Total != 4 || result.NextCursor != tt.nextCursor {
				t.Fatalf("got paths=%v total=%d next=%q", paths, result.Total, result.NextCursor)
			}
		})
	}
}

func TestFileListPageSizeDefaults(t *testing.T) {
	entries := make([]FileEntry, 25)
	for i := range entries {
		entries[i].Path = fmt.Sprintf("%02d", i)
	}
	result := paginateFileResult(FileResult{Items: entries}, FileRequest{Action: "list", Page: 1})
	if len(result.Items) != 20 || result.Total != 25 || result.Items[19].Path != "19" || result.NextCursor != "" {
		t.Fatalf("defaults: count=%d total=%d next=%q", len(result.Items), result.Total, result.NextCursor)
	}
	result = paginateFileResult(FileResult{Items: entries}, FileRequest{Action: "list", Page: 2})
	if len(result.Items) != 5 || result.Total != 25 || result.Items[0].Path != "20" {
		t.Fatalf("second default page: count=%d total=%d", len(result.Items), result.Total)
	}
}

func TestFileListLegacyNoParameters(t *testing.T) {
	entries := make([]FileEntry, 105)
	for i := range entries {
		entries[i].Path = fmt.Sprintf("%03d", i)
	}
	result := paginateFileResult(FileResult{Items: entries}, FileRequest{Action: "list"})
	if len(result.Items) != 100 || result.Total != 105 || result.Items[99].Path != "099" || result.NextCursor != "100" {
		t.Fatalf("legacy defaults: count=%d total=%d next=%q", len(result.Items), result.Total, result.NextCursor)
	}
	result = paginateFileResult(FileResult{Items: entries}, FileRequest{Action: "list", Cursor: "100"})
	if len(result.Items) != 5 || result.Total != 105 || result.Items[0].Path != "100" || result.NextCursor != "" {
		t.Fatalf("legacy second page: count=%d total=%d next=%q", len(result.Items), result.Total, result.NextCursor)
	}
}

func TestFileMutationDigestMatchesLegacyContract(t *testing.T) {
	command := FileCommand{
		TenantID: "11111111-1111-4111-8111-111111111111", SandboxID: "22222222-2222-4222-8222-222222222222",
		OperationID: "33333333-3333-4333-8333-333333333333", IdempotencyKey: "old-key", ExpectedVersion: 1,
		Request: FileRequest{Action: "write", Path: "hello.txt", ContentBase64: "aGVsbG8="},
	}
	got, err := FileRequestDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-pagination digest, retained so existing durable idempotency keys replay.
	const legacy = "fc0e5cfd253ab1d957ad64e317de95b5e2512345f039e3190e2655e85a29a226"
	if got != legacy {
		t.Fatalf("legacy file mutation digest changed: %s", got)
	}
}
