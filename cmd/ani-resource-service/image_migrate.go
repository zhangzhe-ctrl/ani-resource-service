package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	imagedata "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image"
)

func runImageMigration() error {
	path := os.Getenv("ANI_IMAGE_MIGRATION_DSN_FILE")
	role := os.Getenv("ANI_IMAGE_RUNTIME_ROLE")
	if path == "" || role == "" {
		return fmt.Errorf("image-migrate requires ANI_IMAGE_MIGRATION_DSN_FILE and ANI_IMAGE_RUNTIME_ROLE")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("Image migration DSN must be a private regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) > 16384 {
		return fmt.Errorf("Image migration DSN file unavailable or too large")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return imagedata.ApplyImageMigrations(ctx, imagedata.OwnerConfig{DSN: strings.TrimSpace(string(body)), RuntimeRole: role})
}
