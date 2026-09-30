package data

import (
	"context"
	"errors"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var harborName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}[a-z0-9]$`)

type harborProject struct {
	ID       int64  `json:"project_id"`
	Name     string `json:"name"`
	Metadata struct {
		Public string `json:"public"`
	} `json:"metadata"`
}

func (p harborProject) domain() (biz.Project, error) {
	if p.ID <= 0 || !harborName.MatchString(p.Name) || (p.Metadata.Public != "true" && p.Metadata.Public != "false") {
		return biz.Project{}, harborUnavailable()
	}
	return biz.Project{ID: p.ID, Name: p.Name, Private: p.Metadata.Public == "false"}, nil
}
func (h *Harbor) GetProjectByID(ctx context.Context, id int64) (biz.Project, error) {
	if id <= 0 {
		return biz.Project{}, biz.Fail(biz.InvalidArgument, "invalid project ID")
	}
	var p harborProject
	_, err := h.doJSON(ctx, http.MethodGet, "/api/v2.0/projects/"+strconv.FormatInt(id, 10), nil, &p)
	if err != nil {
		return biz.Project{}, err
	}
	if p.ID != id {
		return biz.Project{}, harborUnavailable()
	}
	return p.domain()
}
func (h *Harbor) FindProjectByName(ctx context.Context, name string) (biz.Project, error) {
	if !harborName.MatchString(name) {
		return biz.Project{}, biz.Fail(biz.InvalidArgument, "invalid project name")
	}
	var p harborProject
	_, err := h.doJSON(ctx, http.MethodGet, "/api/v2.0/projects/"+url.PathEscape(name), nil, &p)
	if err != nil {
		return biz.Project{}, err
	}
	if p.Name != name {
		return biz.Project{}, harborUnavailable()
	}
	return p.domain()
}
func (h *Harbor) CreatePrivateProject(ctx context.Context, name string) (biz.Project, error) {
	if !harborName.MatchString(name) {
		return biz.Project{}, biz.Fail(biz.InvalidArgument, "invalid project name")
	}
	request := struct {
		Name     string            `json:"project_name"`
		Metadata map[string]string `json:"metadata"`
	}{name, map[string]string{"public": "false"}}
	header, err := h.doJSON(ctx, http.MethodPost, "/api/v2.0/projects", request, nil)
	if err != nil {
		// Harbor's create handler authenticates/authorizes and validates the
		// request before creating the project. Only complete 400/401/403
		// responses establish a rejected write. Conflicts, 5xx, transport
		// errors and missing success receipts remain uncertain.
		var response *harborResponseError
		if errors.As(err, &response) && (response.status == http.StatusBadRequest || response.status == http.StatusUnauthorized || response.status == http.StatusForbidden) {
			return biz.Project{}, biz.ProjectCreationRejected(err)
		}
		return biz.Project{}, err
	}
	location, err := url.Parse(header.Get("Location"))
	if err != nil || location.RawQuery != "" || location.Fragment != "" || (location.IsAbs() && (location.Scheme != h.base.Scheme || location.Host != h.base.Host)) {
		return biz.Project{}, biz.Fail(biz.SpaceOwnershipUnconfirmed, "created project ID not confirmed")
	}
	const prefix = "/api/v2.0/projects/"
	if !strings.HasPrefix(location.Path, prefix) {
		return biz.Project{}, biz.Fail(biz.SpaceOwnershipUnconfirmed, "created project ID not confirmed")
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(location.Path, prefix), 10, 64)
	if err != nil || id <= 0 {
		return biz.Project{}, biz.Fail(biz.SpaceOwnershipUnconfirmed, "created project ID not confirmed")
	}
	// Return the confirmed Location immediately so callers can persist the ID
	// before any subsequent GET. Never rediscover and adopt by name after POST.
	return biz.Project{ID: id, Name: name, Private: true}, nil
}
