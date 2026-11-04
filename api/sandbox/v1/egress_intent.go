package sandboxv1

import (
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

var egressHostLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeEgressIntent validates and canonicalizes recorded user intent. It
// does not resolve names or imply that any network rule is applied.
func NormalizeEgressIntent(in *SandboxEgressIntent) (*SandboxEgressIntent, error) {
	if in == nil {
		return nil, nil
	}
	if len(in.ProtoReflect().GetUnknown()) != 0 || len(in.EgressAllowlist) > 100 {
		return nil, errors.New("INVALID_SANDBOX_EGRESS_INTENT")
	}
	policy := in.NetworkEgressPolicy
	if policy != "allowlist" && policy != "deny_all" && policy != "internet" {
		return nil, errors.New("INVALID_SANDBOX_EGRESS_INTENT")
	}
	if policy != "allowlist" && len(in.EgressAllowlist) > 0 {
		return nil, errors.New("INVALID_SANDBOX_EGRESS_INTENT")
	}
	hosts := make([]string, 0, len(in.EgressAllowlist))
	seen := map[string]bool{}
	for _, raw := range in.EgressAllowlist {
		host := strings.ToLower(strings.TrimSpace(raw))
		if host == "" || len(host) > 253 {
			return nil, errors.New("INVALID_SANDBOX_EGRESS_HOST")
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			if ip.Zone() != "" {
				return nil, errors.New("INVALID_SANDBOX_EGRESS_HOST")
			}
			host = ip.String()
		} else {
			wildcard := strings.HasPrefix(host, "*.")
			plain := host
			if wildcard {
				plain = host[2:]
			}
			if strings.HasSuffix(plain, ".") {
				plain = strings.TrimSuffix(plain, ".")
			}
			if plain == "" {
				return nil, errors.New("INVALID_SANDBOX_EGRESS_HOST")
			}
			for _, label := range strings.Split(plain, ".") {
				if !egressHostLabel.MatchString(label) {
					return nil, errors.New("INVALID_SANDBOX_EGRESS_HOST")
				}
			}
			host = plain
			if wildcard {
				host = "*." + plain
			}
		}
		if seen[host] {
			return nil, errors.New("DUPLICATE_SANDBOX_EGRESS_HOST")
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return &SandboxEgressIntent{NetworkEgressPolicy: policy, EgressAllowlist: hosts}, nil
}

// EncodeEgressIntent preserves legacy absent snapshots as an empty string.
func EncodeEgressIntent(in *SandboxEgressIntent) (string, error) {
	normalized, err := NormalizeEgressIntent(in)
	if err != nil || normalized == nil {
		return "", err
	}
	b, err := json.Marshal(normalized)
	return string(b), err
}
func DecodeEgressIntent(raw string) (*SandboxEgressIntent, error) {
	if raw == "" {
		return nil, nil
	}
	var value SandboxEgressIntent
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	normalized, err := NormalizeEgressIntent(&value)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil || string(encoded) != raw {
		return nil, errors.New("INVALID_STORED_SANDBOX_EGRESS_INTENT")
	}
	return normalized, nil
}
