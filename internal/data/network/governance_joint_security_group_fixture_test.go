//go:build networkintegration

package data_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	controlled "github.com/zhangzhe-ctrl/ani-resource-service/tests/net05a/provider"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"k8s.io/apimachinery/pkg/labels"
)

// Only the external Kubernetes/KCN API contract is controlled. This boundary
// never touches SQL or calls a Resource use case/worker in place of the product.
type networkJointSecurityGroupBoundary struct{ mu sync.Mutex }

func (b *networkJointSecurityGroupBoundary) refresh(api *controlled.Server) {
	for _, group := range jointObjects(api, "securitygroups") {
		meta := group["metadata"].(map[string]any)
		spec, _ := group["spec"].(map[string]any)
		members := map[string]any{}
		for _, eip := range jointObjects(api, "eips") {
			em := eip["metadata"].(map[string]any)
			es, _ := eip["spec"].(map[string]any)
			status, _ := eip["status"].(map[string]any)
			if em["namespace"] != meta["namespace"] || status["phase"] != "Bound" {
				continue
			}
			groups, _ := es["securityGroups"].([]any)
			for _, name := range groups {
				if name == meta["name"] {
					members[fmt.Sprint(em["name"])] = fmt.Sprint(em["uid"])
				}
			}
		}
		status := map[string]any{"conditions": []any{map[string]any{"type": "Valid", "status": "True"}, map[string]any{"type": "Ready", "status": "True"}}, "observedGeneration": meta["generation"], "allowSameGroupTraffic": spec["allowSameGroupTraffic"], "appliedVNics": []any{}, "appliedEIPs": members}
		before, _ := json.Marshal(group["status"])
		after, _ := json.Marshal(status)
		if string(before) != string(after) {
			group["status"] = status
			api.Change("securitygroups", group, false)
		}
	}
}
func (b *networkJointSecurityGroupBoundary) http(w http.ResponseWriter, r *http.Request, api *controlled.Server) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 6 || parts[0] != "apis" || parts[1] != "networking.kubercloud.com" || parts[2] != "v1" || parts[3] != "namespaces" {
		return false
	}
	resource, namespace := parts[5], parts[4]
	if resource != "securitygroups" && (resource != "eips" || r.Method != http.MethodPatch) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refresh(api)
	w.Header().Set("Content-Type", "application/json")
	fail := func(code int, reason string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": reason, "code": code})
	}
	name := ""
	if len(parts) == 7 {
		name = parts[6]
	} else if len(parts) != 6 {
		fail(404, "NotFound")
		return true
	}
	if r.Method == http.MethodGet && name == "" {
		selector, err := labels.Parse(r.URL.Query().Get("labelSelector"))
		if err != nil {
			fail(400, "BadRequest")
			return true
		}
		items := []any{}
		for _, obj := range jointObjects(api, "securitygroups") {
			meta := obj["metadata"].(map[string]any)
			set := labels.Set{}
			raw, _ := meta["labels"].(map[string]any)
			for k, v := range raw {
				set[k] = fmt.Sprint(v)
			}
			if meta["namespace"] == namespace && selector.Matches(set) {
				items = append(items, obj)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "SecurityGroupList", "metadata": map[string]any{}, "items": items})
		return true
	}
	object := api.Object(resource, namespace, name)
	switch r.Method {
	case http.MethodGet:
		if object == nil {
			fail(404, "NotFound")
		} else {
			_ = json.NewEncoder(w).Encode(object)
		}
	case http.MethodPost:
		if resource != "securitygroups" || name != "" {
			fail(405, "MethodNotAllowed")
			break
		}
		if json.NewDecoder(r.Body).Decode(&object) != nil {
			fail(400, "BadRequest")
			break
		}
		meta, _ := object["metadata"].(map[string]any)
		if meta == nil || meta["namespace"] != namespace || object["apiVersion"] != "networking.kubercloud.com/v1" || object["kind"] != "SecurityGroup" {
			fail(422, "Invalid")
			break
		}
		name, _ = meta["name"].(string)
		if name == "" {
			fail(422, "Invalid")
			break
		}
		if api.Object(resource, namespace, name) != nil {
			fail(409, "AlreadyExists")
			break
		}
		meta["uid"], meta["generation"] = uuid.NewString(), 1
		api.Change(resource, object, false)
		b.refresh(api)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(api.Object(resource, namespace, name))
	case http.MethodPatch:
		if object == nil {
			fail(404, "NotFound")
			break
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil || len(raw) > 65536 {
			fail(400, "BadRequest")
			break
		}
		var operations []struct {
			Op, Path string
			Value    any
		}
		if json.Unmarshal(raw, &operations) != nil || len(operations) != 3 || operations[0].Op != "test" || operations[0].Path != "/metadata/uid" || operations[1].Op != "test" || operations[1].Path != "/metadata/resourceVersion" || operations[2].Op != "add" || (resource == "eips" && operations[2].Path != "/spec/securityGroups") || (resource == "securitygroups" && operations[2].Path != "/spec") {
			fail(422, "Invalid")
			break
		}
		patch, err := jsonpatch.DecodePatch(raw)
		if err != nil {
			fail(400, "BadRequest")
			break
		}
		previous, _ := json.Marshal(object)
		updated, err := patch.Apply(previous)
		if err != nil {
			fail(409, "Conflict")
			break
		}
		if json.Unmarshal(updated, &object) != nil {
			fail(400, "BadRequest")
			break
		}
		meta := object["metadata"].(map[string]any)
		generation, _ := strconv.Atoi(fmt.Sprint(meta["generation"]))
		meta["generation"] = generation + 1
		api.Change(resource, object, false)
		b.refresh(api)
		_ = json.NewEncoder(w).Encode(api.Object(resource, namespace, name))
	case http.MethodDelete:
		if object == nil {
			fail(404, "NotFound")
			break
		}
		var options struct {
			Preconditions struct{ UID, ResourceVersion string }
		}
		if json.NewDecoder(r.Body).Decode(&options) != nil {
			fail(400, "BadRequest")
			break
		}
		meta := object["metadata"].(map[string]any)
		if options.Preconditions.UID != meta["uid"] || options.Preconditions.ResourceVersion != meta["resourceVersion"] {
			fail(409, "Conflict")
			break
		}
		api.Change(resource, object, true)
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Success"})
	default:
		fail(405, "MethodNotAllowed")
	}
	return true
}
