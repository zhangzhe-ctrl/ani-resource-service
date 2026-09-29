package main

import (
	"context"
	conf "github.com/zhangzhe-ctrl/ani-resource-service/internal/conf/v1"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageDisabledAndPrivateFiles(t *testing.T) {
	for _, c := range []*conf.Image{nil, {Enabled: false, HarborPasswordFile: "/does/not/exist"}} {
		v, err := openImage(context.Background(), c)
		if err != nil || v != nil {
			t.Fatal("disabled Image opened dependencies")
		}
		v.Close()
	}
	p := filepath.Join(t.TempDir(), "private")
	sentinel := "never-log-this-sentinel"
	if err := os.WriteFile(p, []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := readImagePrivateFile(p)
	if err != nil || string(b) != sentinel {
		t.Fatal("private file rejected", err)
	}
	clear(b)
	link := p + "-link"
	if err = os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err = readImagePrivateFile(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err = os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = readImagePrivateFile(p); err == nil || strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), p) {
		t.Fatal("unsafe permissions or error disclosure")
	}
	if err = os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, make([]byte, 65537), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readImagePrivateFile(p); err == nil {
		t.Fatal("oversized file accepted")
	}
}
