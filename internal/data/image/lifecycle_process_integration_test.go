//go:build imageintegration

package data_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	imagedata "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image"
)

type robotWire struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Level       string           `json:"level"`
	Duration    int64            `json:"duration"`
	ExpiresAt   int64            `json:"expires_at"`
	Editable    bool             `json:"editable"`
	Disable     bool             `json:"disable"`
	Permissions []permissionWire `json:"permissions"`
}
type permissionWire struct {
	Kind      string       `json:"kind"`
	Namespace string       `json:"namespace"`
	Access    []accessWire `json:"access"`
}
type accessWire struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Effect   string `json:"effect"`
}

func toRobotWire(r biz.Robot) robotWire {
	w := robotWire{ID: r.ID, Name: r.Name, Description: r.Description, Level: "system", Duration: r.DurationDays, ExpiresAt: r.ExpiresAt, Editable: true, Disable: r.Disabled}
	for _, p := range r.Permissions {
		w.Permissions = append(w.Permissions, permissionWire{Kind: "project", Namespace: p.Project, Access: []accessWire{{p.Resource, p.Action, "allow"}}})
	}
	return w
}
func registryHTTP(t *testing.T, r *registryFixture) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		user, password, ok := request.BasicAuth()
		if !ok || user != "fixture-admin" || password != "fixture-password" {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		ctx := request.Context()
		path := request.URL.Path
		respond := func(value any, err error) {
			if err != nil {
				if biz.ReasonOf(err) == biz.ImageNotFound {
					w.WriteHeader(404)
				} else {
					w.WriteHeader(409)
				}
				_, _ = w.Write([]byte(`{}`))
				return
			}
			if value == nil {
				value = struct{}{}
			}
			_ = json.NewEncoder(w).Encode(value)
		}
		if path == "/api/v2.0/projects" && request.Method == "POST" {
			var input struct {
				Name     string            `json:"project_name"`
				Metadata map[string]string `json:"metadata"`
			}
			if json.NewDecoder(request.Body).Decode(&input) != nil || input.Metadata["public"] != "false" {
				w.WriteHeader(400)
				return
			}
			project, err := r.CreatePrivateProject(ctx, input.Name)
			if err != nil {
				respond(nil, err)
				return
			}
			w.Header().Set("Location", fmt.Sprintf("/api/v2.0/projects/%d", project.ID))
			w.WriteHeader(201)
			return
		}
		if strings.HasPrefix(path, "/api/v2.0/projects/") && request.Method == "GET" {
			key := strings.TrimPrefix(path, "/api/v2.0/projects/")
			id, err := strconv.ParseInt(key, 10, 64)
			var project biz.Project
			if err == nil {
				project, err = r.GetProjectByID(ctx, id)
			} else {
				project, err = r.FindProjectByName(ctx, key)
			}
			respond(map[string]any{"project_id": project.ID, "name": project.Name, "metadata": map[string]string{"public": strconv.FormatBool(!project.Private)}}, err)
			return
		}
		if path == "/api/v2.0/robots" {
			if request.Method == "POST" {
				var input robotWire
				if json.NewDecoder(request.Body).Decode(&input) != nil || input.Level != "system" {
					w.WriteHeader(400)
					return
				}
				want := biz.RobotRequest{Name: input.Name, Description: input.Description, DurationDays: input.Duration}
				for _, p := range input.Permissions {
					if p.Kind != "project" {
						w.WriteHeader(400)
						return
					}
					for _, a := range p.Access {
						if a.Effect != "allow" {
							w.WriteHeader(400)
							return
						}
						want.Permissions = append(want.Permissions, biz.RobotPermission{Project: p.Namespace, Resource: a.Resource, Action: a.Action})
					}
				}
				robot, err := r.CreateRobot(ctx, want)
				if err != nil {
					respond(nil, err)
					return
				}
				w.WriteHeader(201)
				respond(map[string]any{"id": robot.ID, "name": robot.Name, "expires_at": robot.ExpiresAt, "secret": "discarded-fixture-secret"}, nil)
				return
			}
			if request.Method == "GET" {
				name := strings.TrimPrefix(request.URL.Query().Get("q"), "Level=system,Name=")
				rows := []robotWire{}
				r.mu.Lock()
				for _, robot := range r.robots {
					if robot.Name == name {
						rows = append(rows, toRobotWire(robot))
					}
				}
				r.mu.Unlock()
				respond(rows, nil)
				return
			}
		}
		if strings.HasPrefix(path, "/api/v2.0/robots/") {
			id, err := strconv.ParseInt(strings.TrimPrefix(path, "/api/v2.0/robots/"), 10, 64)
			if err != nil {
				w.WriteHeader(400)
				return
			}
			robot, err := r.GetRobot(ctx, id)
			if err != nil {
				respond(nil, err)
				return
			}
			switch request.Method {
			case "GET":
				respond(toRobotWire(robot), nil)
			case "PATCH":
				var body struct {
					Secret string `json:"secret"`
				}
				if json.NewDecoder(request.Body).Decode(&body) != nil {
					w.WriteHeader(400)
					return
				}
				respond(struct{}{}, r.SetRobotSecret(ctx, robot, biz.Secret(body.Secret)))
			case "PUT":
				var body robotWire
				if json.NewDecoder(request.Body).Decode(&body) != nil {
					w.WriteHeader(400)
					return
				}
				if body.Name != robot.Name || body.Duration != robot.DurationDays {
					w.WriteHeader(400)
					return
				}
				respond(struct{}{}, r.SetRobotDisabled(ctx, robot, body.Disable))
			default:
				w.WriteHeader(405)
			}
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(server.Close)
	return server
}

type crashInput struct {
 Reset bool
	DSN, URL   string
	CA         []byte
	Config     biz.LifecycleConfig
	Tenant, At string
}
type crashRepository struct {
	biz.LifecycleRepository
	at string
}

func (r *crashRepository) hit(at string) {
	if r.at == at {
		os.Exit(73)
	}
}
func (r *crashRepository) BindTenantProject(ctx context.Context, s biz.Space, id int64) (biz.Space, error) {
	v, e := r.LifecycleRepository.BindTenantProject(ctx, s, id)
	if e == nil {
		r.hit("project_bind")
	}
	return v, e
}
func (r *crashRepository) PrepareTenantCandidate(ctx context.Context, c biz.Command) (biz.Command, error) {
	v, e := r.LifecycleRepository.PrepareTenantCandidate(ctx, c)
	if e == nil {
		r.hit(c.Candidate.Purpose + ":candidate_prepared")
	}
	return v, e
}
func (r *crashRepository) SaveTenantCommandPhase(ctx context.Context, c biz.Command) (biz.Command, error) {
	v, e := r.LifecycleRepository.SaveTenantCommandPhase(ctx, c)
	if e == nil {
		r.hit(c.Candidate.Purpose + ":" + c.Phase)
	}
	return v, e
}
func (r *crashRepository) ActivateTenantCandidate(ctx context.Context, s biz.Space, c biz.Command, secret biz.EncryptedSecret) (biz.Space, biz.Command, error) {
	v, k, e := r.LifecycleRepository.ActivateTenantCandidate(ctx, s, c, secret)
	if e == nil {
		r.hit(c.Candidate.Purpose + ":activate")
	}
	return v, k, e
}

type crashRegistry struct {
	biz.Registry
	at string
}

func (r *crashRegistry) hit(purpose, stage string) {
	if r.at == purpose+":"+stage {
		os.Exit(73)
	}
}
func robotPurpose(description string) string {
	if strings.Contains(description, ":pull:") {
		return "pull"
	}
	return "publisher"
}
func (r *crashRegistry) CreateRobot(ctx context.Context, in biz.RobotRequest) (biz.Robot, error) {
	v, e := r.Registry.CreateRobot(ctx, in)
	if e == nil {
		r.hit(robotPurpose(in.Description), "robot_create")
	}
	return v, e
}
func (r *crashRegistry) SetRobotSecret(ctx context.Context, in biz.Robot, secret biz.Secret) error {
	e := r.Registry.SetRobotSecret(ctx, in, secret)
	if e == nil {
		r.hit(robotPurpose(in.Description), "secret_set_provider")
	}
	return e
}

func(r *crashRegistry)SetRobotDisabled(ctx context.Context,in biz.Robot,disabled bool)error{e:=r.Registry.SetRobotDisabled(ctx,in,disabled);if e==nil{r.hit(robotPurpose(in.Description),"previous_disable_provider")};return e}

// Entered only by our exact test binary subprocess; missing helper configuration
// does not skip any acceptance test (the parent cases below always execute).
func TestLifecycleCrashHelper(t *testing.T) {
	path := os.Getenv("IMAGE_LIFECYCLE_CHILD_CONFIG")
	if path == "" {
		return
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("private child input required")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("child input unavailable")
	}
	var in crashInput
	if json.Unmarshal(body, &in) != nil {
		t.Fatal("invalid child input")
	}
	clear(body)
	repo, err := imagedata.OpenPostgres(context.Background(), in.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	harbor, err := imagedata.NewHarbor(imagedata.HarborConfig{URL: in.URL, CAPEM: in.CA, Username: "fixture-admin", Password: biz.Secret("fixture-password"), RobotNamePrefix: "fixture$"})
	if err != nil {
		t.Fatal(err)
	}
	defer harbor.Close()
	ring, err := imagedata.NewAESGCMKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("k", 32))})
	if err != nil {
		t.Fatal(err)
	}
	cr:=&crashRepository{LifecycleRepository:repo,at:in.At};hr:=&crashRegistry{Registry:harbor,at:in.At};if in.Reset{cr.at="";hr.at=""};l := lifecycle(t, cr, hr, ring, in.Config, nil)
	ctx := biz.WithCaller(context.Background(), biz.Caller{Kind: biz.GovernanceCaller, Subject: "governance", Actor: "user:test", TenantID: in.Tenant})
	if _, err = l.EnsureImageSpace(ctx, biz.EnableSpace{TenantID: in.Tenant, Slug: "process", IdempotencyKey: "process-enable"}); err != nil {
		t.Fatal(err)
	}
	if _, err = l.IssuePublisherCredential(ctx, biz.IssueCredential{TenantID: in.Tenant, IdempotencyKey: "process-issue", ExpectedVersion: 0}); err != nil && !(in.Reset && biz.ReasonOf(err)==biz.CredentialDeliveryExpired) {
		t.Fatal(err)
	}
 if in.Reset {s,e:=repo.FindTenantSpace(ctx,in.Tenant);if e!=nil{t.Fatal(e)};original,e:=repo.FindTenantCommand(ctx,in.Tenant,s.ID,"process-issue");if e!=nil||original.Result.Credential==nil{t.Fatal("original issue not recorded")};cr.at=strings.TrimPrefix(in.At,"reset:");hr.at=cr.at;if _,e=l.ResetPublisherCredential(ctx,biz.ResetCredential{TenantID:in.Tenant,IdempotencyKey:"process-reset",ExpectedVersion:original.Result.Credential.Version});e!=nil{t.Fatal(e)}}
}
func childFile(t *testing.T, in crashInput) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "child.json")
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal("child encoding")
	}
	defer clear(body)
	if err = os.WriteFile(path, body, 0600); err != nil {
		t.Fatal("child input write")
	}
	return path
}
func runLifecycleChild(t *testing.T, in crashInput, expected int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLifecycleCrashHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "IMAGE_LIFECYCLE_CHILD_CONFIG="+childFile(t, in))
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal("child failed to start")
		}
		code = exit.ExitCode()
	}
	if code != expected {
		t.Fatalf("child exit=%d expected=%d output=%s", code, expected, output)
	}
	t.Logf("owned process pid=%d boundary=%s exit=%d", cmd.Process.Pid, in.At, code)
}
func TestLifecycleActualProcessExitRecovery(t *testing.T) {
	for _, at := range []string{"project_bind", "pull:candidate_prepared", "pull:robot_create", "pull:secret_set_provider", "publisher:robot_create", "publisher:robot_created", "publisher:secret_set_provider", "publisher:previous_disabled", "publisher:activate", "reset:publisher:secret_set_provider", "reset:publisher:previous_disable_provider", "reset:publisher:previous_disabled", "reset:publisher:activate"} {
		t.Run(at, func(t *testing.T) {
			f, r, cfg, _, ctx := lifecycleFixture(t)
			server := registryHTTP(t, r)
			in := crashInput{DSN: f.RuntimeDSN, URL: server.URL, CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), Config: cfg, Tenant: tenantFrom(t, ctx), At: at, Reset:strings.HasPrefix(at,"reset:")}
			runLifecycleChild(t, in, 73)
			in.At = ""
			runLifecycleChild(t, in, 0)
			info, err := f.Repo.FindTenantSpace(ctx, in.Tenant)
			if err != nil || info.State != "available" {
				t.Fatal("space not recovered", err)
			}
			credential, err := f.Repo.GetTenantPublisher(ctx, in.Tenant, info.ID)
			expectedGeneration:=int64(1);expectedRobots:=2;if in.Reset{expectedGeneration=2;expectedRobots=3}
			if err != nil || credential.Generation != expectedGeneration || credential.State != "active" {
				t.Fatal("credential not recovered", err)
			}
			r.mu.Lock()
			projects, robots := r.creates, r.robotCreates
			r.mu.Unlock()
			if projects != 1 || robots != expectedRobots {
				t.Fatal("process restart duplicated provider identities")
			}
		})
	}
}
