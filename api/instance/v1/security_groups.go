package instancev1

import (
	"encoding/json"
	"github.com/google/uuid"
	"regexp"
)

var securityGroupIDPattern = regexp.MustCompile(`^sg_[0-9a-f]{32}$`)

// ValidateSecurityGroupIDs preserves selection order and rejects ambiguous repeats.
func ValidateSecurityGroupIDs(ids []string) error {
	if len(ids) > 16 {
		return ErrInvalidBusiness
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !securityGroupIDPattern.MatchString(id) || seen[id] {
			return ErrInvalidBusiness
		}
		seen[id] = true
	}
	return nil
}

// CanonicalSecurityGroupIntent binds version and complete replacement selection.
func CanonicalSecurityGroupIntent(id string, version int64, ids []string) ([]byte, error) {
	u, e := uuid.Parse(id)
	if e != nil || u == uuid.Nil || u.String() != id || version < 1 || ValidateSecurityGroupIDs(ids) != nil {
		return nil, ErrInvalidBusiness
	}
	selected := append([]string{}, ids...)
	return json.Marshal([]any{id, version, "set-security-groups", selected})
}
