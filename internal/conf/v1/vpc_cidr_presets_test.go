package conf

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/file"
)

func TestVPCCIDRPresetsYAML(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       []string
		valid      bool
	}{
		{"configured", "network:\n  vpc_cidr_presets: [10.61.0.0/16, 10.62.0.0/16]\n", []string{"10.61.0.0/16", "10.62.0.0/16"}, true},
		{"empty", "network:\n  vpc_cidr_presets: []\n", []string{}, true},
		{"duplicate", "network:\n  vpc_cidr_presets: [10.61.0.0/16, 10.61.0.0/16]\n", []string{"10.61.0.0/16", "10.61.0.0/16"}, false},
		{"host-prefix", "network:\n  vpc_cidr_presets: [10.61.0.1/16]\n", []string{"10.61.0.1/16"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			c := config.New(config.WithSource(file.NewSource(path)))
			defer c.Close()
			if err := c.Load(); err != nil {
				t.Fatal(err)
			}
			var parsed Bootstrap
			if err := c.Scan(&parsed); err != nil {
				t.Fatal(err)
			}
			if parsed.Network == nil || !slices.Equal(parsed.Network.VpcCidrPresets, tc.want) {
				t.Fatal("startup YAML did not load presets", parsed.Network)
			}
			cfg := validConfig()
			cfg.Network.VpcCidrPresets = parsed.Network.VpcCidrPresets
			if err := cfg.Validate(); (err == nil) != tc.valid {
				t.Fatal("startup preset validation", err)
			}
		})
	}
}
