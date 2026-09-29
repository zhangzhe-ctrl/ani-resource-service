package data

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
)

type harborAccess struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Effect   string `json:"effect"`
}
type harborPermission struct {
	Kind      string         `json:"kind"`
	Namespace string         `json:"namespace"`
	Access    []harborAccess `json:"access"`
}
type harborRobot struct {
	ID          int64              `json:"id,omitempty"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Level       string             `json:"level"`
	Duration    int64              `json:"duration"`
	Disable     bool               `json:"disable"`
	Editable    bool               `json:"editable,omitempty"`
	ExpiresAt   int64              `json:"expires_at,omitempty"`
	Permissions []harborPermission `json:"permissions"`
	CoverAll    bool               `json:"cover_all,omitempty"`
}

var robotName = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

func robotDenied() error {
	return biz.Fail(biz.PermissionDenied, "robot ownership or permissions unconfirmed")
}
func robotPermissions(in []biz.RobotPermission) ([]harborPermission, error) {
	if err := biz.ValidateRobotPermissions(in, in); err != nil {
		return nil, err
	}
	out := []harborPermission{}
	for _, v := range in {
		if !harborName.MatchString(v.Project) {
			return nil, robotDenied()
		}
		index := -1
		for i := range out {
			if out[i].Namespace == v.Project {
				index = i
				break
			}
		}
		if index < 0 {
			out = append(out, harborPermission{Kind: "project", Namespace: v.Project})
			index = len(out) - 1
		}
		out[index].Access = append(out[index].Access, harborAccess{v.Resource, v.Action, "allow"})
	}
	return out, nil
}
func (r harborRobot) domain() (biz.Robot, error) {
	if r.ID <= 0 || r.Name == "" || r.Level != "system" || r.CoverAll || !r.Editable || (r.ExpiresAt <= 0 && r.ExpiresAt != -1) {
		return biz.Robot{}, robotDenied()
	}
	permissions := []biz.RobotPermission{}
	for _, p := range r.Permissions {
		if p.Kind != "project" || !harborName.MatchString(p.Namespace) || len(p.Access) == 0 {
			return biz.Robot{}, robotDenied()
		}
		for _, a := range p.Access {
			if a.Effect != "allow" {
				return biz.Robot{}, robotDenied()
			}
			permissions = append(permissions, biz.RobotPermission{Project: p.Namespace, Resource: a.Resource, Action: a.Action})
		}
	}
	if err := biz.ValidateRobotPermissions(permissions, permissions); err != nil {
		return biz.Robot{}, err
	}
	return biz.Robot{ID: r.ID, Name: r.Name, Username: r.Name, Description: r.Description, Disabled: r.Disable, Permissions: permissions, ExpiresAt: r.ExpiresAt}, nil
}
func (h *Harbor) rawRobot(ctx context.Context, id int64) (harborRobot, error) {
	var r harborRobot
	if id <= 0 {
		return r, robotDenied()
	}
	_, err := h.doJSON(ctx, http.MethodGet, "/api/v2.0/robots/"+strconv.FormatInt(id, 10), nil, &r)
	if err != nil {
		return r, err
	}
	if r.ID != id {
		return r, robotDenied()
	}
	return r, nil
}
func (h *Harbor) GetRobot(ctx context.Context, id int64) (biz.Robot, error) {
	r, err := h.rawRobot(ctx, id)
	if err != nil {
		return biz.Robot{}, err
	}
	return r.domain()
}
func (h *Harbor) validateRobotRequest(r biz.RobotRequest) error {
	if !robotName.MatchString(r.Name) || len(r.Name) > 128 || !strings.HasPrefix(r.Description, "ani-image:v1:") || len(r.Description) > 512 || strings.ContainsAny(r.Description, "\r\n") || (r.DurationDays != -1 && (r.DurationDays < 1 || r.DurationDays > 3650)) {
		return robotDenied()
	}
	_, err := robotPermissions(r.Permissions)
	return err
}
func (h *Harbor) matches(r biz.Robot, want biz.RobotRequest) error {
	if r.Name != h.robotPrefix+want.Name || r.Username != r.Name || r.Description != want.Description {
		return robotDenied()
	}
	return biz.ValidateRobotPermissions(r.Permissions, want.Permissions)
}
func (h *Harbor) CreateRobot(ctx context.Context, want biz.RobotRequest) (biz.Robot, error) {
	if err := h.validateRobotRequest(want); err != nil {
		return biz.Robot{}, err
	}
	permissions, err := robotPermissions(want.Permissions)
	if err != nil {
		return biz.Robot{}, err
	}
	request := harborRobot{Name: want.Name, Description: want.Description, Level: "system", Duration: want.DurationDays, Permissions: permissions}
	// The random creation secret is deliberately ignored; command recovery owns a
	// pre-encrypted replacement secret, applied later via PATCH.
	var created struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if _, err = h.doJSON(ctx, http.MethodPost, "/api/v2.0/robots", request, &created); err != nil {
		return biz.Robot{}, err
	}
	if created.ID <= 0 || created.Name != h.robotPrefix+want.Name {
		return biz.Robot{}, robotDenied()
	}
	// Caller persists ID before verification; no second request can erase that ID.
	return biz.Robot{ID: created.ID, Name: created.Name, Username: created.Name, Description: want.Description, Permissions: want.Permissions, ExpiresAt: created.ExpiresAt}, nil
}
func (h *Harbor) FindOwnedRobot(ctx context.Context, want biz.RobotRequest) (biz.Robot, error) {
	if err := h.validateRobotRequest(want); err != nil {
		return biz.Robot{}, err
	}
	values := url.Values{"q": {"Level=system,Name=" + h.robotPrefix + want.Name}, "page": {"1"}, "page_size": {"2"}}
	var rows []harborRobot
	if _, err := h.doJSON(ctx, http.MethodGet, "/api/v2.0/robots?"+values.Encode(), nil, &rows); err != nil {
		return biz.Robot{}, err
	}
	if len(rows) == 0 {
		return biz.Robot{}, biz.Fail(biz.ImageNotFound, "robot not found")
	}
	if len(rows) != 1 {
		return biz.Robot{}, robotDenied()
	}
	r, err := rows[0].domain()
	if err != nil {
		return biz.Robot{}, err
	}
	if err = h.matches(r, want); err != nil {
		return biz.Robot{}, err
	}
	return r, nil
}
func (h *Harbor) ownedRobot(ctx context.Context, want biz.Robot) (harborRobot, error) {
	r, err := h.rawRobot(ctx, want.ID)
	if err != nil {
		return r, err
	}
	actual, err := r.domain()
	if err != nil {
		return r, err
	}
	if actual.ID != want.ID || actual.Name != want.Name || actual.Username != want.Username || actual.Description != want.Description || !strings.HasPrefix(want.Description, "ani-image:v1:") {
		return r, robotDenied()
	}
	if err = biz.ValidateRobotPermissions(actual.Permissions, want.Permissions); err != nil {
		return r, err
	}
	return r, nil
}
func (h *Harbor) SetRobotSecret(ctx context.Context, want biz.Robot, secret biz.Secret) error {
	if len(secret) < 32 || len(secret) > 128 {
		return biz.Fail(biz.InvalidArgument, "invalid robot secret length")
	}
	upper, lower, digit := false, false, false
	for _, v := range secret {
		upper = upper || (v >= 'A' && v <= 'Z')
		lower = lower || (v >= 'a' && v <= 'z')
		digit = digit || (v >= '0' && v <= '9')
	}
	if !upper || !lower || !digit {
		return biz.Fail(biz.InvalidArgument, "invalid robot secret strength")
	}
	if _, err := h.ownedRobot(ctx, want); err != nil {
		return err
	}
	_, err := h.doJSON(ctx, http.MethodPatch, "/api/v2.0/robots/"+strconv.FormatInt(want.ID, 10), struct {
		Secret string `json:"secret"`
	}{string(secret)}, nil)
	return err
}
func (h *Harbor) SetRobotDisabled(ctx context.Context, want biz.Robot, disabled bool) error {
	r, err := h.ownedRobot(ctx, want)
	if err != nil {
		return err
	}
	if r.Disable == disabled {
		return nil
	}
	r.Disable = disabled
	if _, err = h.doJSON(ctx, http.MethodPut, "/api/v2.0/robots/"+strconv.FormatInt(want.ID, 10), r, nil); err != nil {
		return err
	}
	after, err := h.ownedRobot(ctx, want)
	if err != nil {
		return err
	}
	if after.Disable != disabled {
		return harborUnavailable()
	}
	return nil
}
