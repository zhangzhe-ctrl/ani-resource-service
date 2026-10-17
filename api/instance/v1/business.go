package instancev1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// CanonicalBusinessPayload is shared by Gov and Res. Trusted attachments and
// command identities are separate messages and never enter the public hash.
func CanonicalBusinessPayload(m proto.Message) ([]byte, error) {
	switch m.(type) {
	case *CreateInstanceData, *CreateSpec:
	default:
		return nil, errors.New("unsupported instance business message")
	}
	if m == nil || !m.ProtoReflect().IsValid() || hasUnknown(m.ProtoReflect()) {
		return nil, errors.New("invalid instance business")
	}
	raw, e := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(m)
	if e != nil {
		return nil, e
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if e = dec.Decode(&value); e != nil {
		return nil, e
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if e = enc.Encode(value); e != nil {
		return nil, e
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}
func BusinessPayloadDigest(m proto.Message) (string, error) {
	raw, e := CanonicalBusinessPayload(m)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func DecodeBusinessPayload(raw []byte) (*CreateSpec, error) {
	b := new(CreateSpec)
	if e := protojson.Unmarshal(raw, b); e != nil {
		return nil, e
	}
	if e := ValidateCreateSpec(b, b.GpuExecution != nil); e != nil {
		return nil, e
	}
	return b, nil
}

var ErrInvalidBusiness = errors.New("INVALID_INSTANCE_REQUEST")
var ErrInvalidScheduling = errors.New("INVALID_SCHEDULING_OPTIONS")
var ErrSchedulingConflict = errors.New("SCHEDULING_FIELD_CONFLICT")

var runtimeClassLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ValidateGPURuntimeClassName checks the managed owner RuntimeClass name.
func ValidateGPURuntimeClassName(name string) error {
	if len(name) == 0 || len(name) > 253 {
		return ErrInvalidBusiness
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) > 63 || !runtimeClassLabel.MatchString(label) {
			return ErrInvalidBusiness
		}
	}
	return nil
}

// ValidateSchedulingOptions preserves optional presence. Effective options always
// contain a concrete placement; raw intent may omit the whole object.
func ValidateSchedulingOptions(s *SchedulingOptions, effective bool) error {
	if s == nil {
		if effective {
			return ErrInvalidScheduling
		}
		return nil
	}
	if len(s.ProtoReflect().GetUnknown()) != 0 || (s.PodPlacement == nil && s.PriorityClassName == nil) {
		return ErrInvalidScheduling
	}
	if effective && s.PodPlacement == nil {
		return ErrInvalidScheduling
	}
	if s.PodPlacement != nil && (*s.PodPlacement < SchedulingOptions_NONE || *s.PodPlacement > SchedulingOptions_SPREAD_PREFERRED) {
		return ErrInvalidScheduling
	}
	if s.PriorityClassName != nil && (strings.TrimSpace(*s.PriorityClassName) != *s.PriorityClassName || *s.PriorityClassName == "") {
		return ErrInvalidScheduling
	}
	return nil
}
func ResolveSchedulingOptions(raw, defaults *SchedulingOptions) (*SchedulingOptions, error) {
	if err := ValidateSchedulingOptions(raw, false); err != nil {
		return nil, err
	}
	if defaults != nil {
		if err := ValidateSchedulingOptions(defaults, false); err != nil {
			return nil, err
		}
	}
	out := &SchedulingOptions{PodPlacement: SchedulingOptions_NONE.Enum()}
	for _, s := range []*SchedulingOptions{defaults, raw} {
		if s == nil {
			continue
		}
		if s.PodPlacement != nil {
			out.PodPlacement = s.GetPodPlacement().Enum()
		}
		if s.PriorityClassName != nil {
			v := s.GetPriorityClassName()
			out.PriorityClassName = &v
		}
	}
	return out, nil
}
func ValidateSchedulingConsistency(raw, effective *SchedulingOptions) error {
	if err := ValidateSchedulingOptions(effective, true); err != nil {
		return err
	}
	if err := ValidateSchedulingOptions(raw, false); err != nil {
		return err
	}
	if raw == nil {
		return nil
	}
	if raw.PodPlacement != nil && raw.GetPodPlacement() != effective.GetPodPlacement() {
		return fmt.Errorf("%w: pod_placement", ErrSchedulingConflict)
	}
	if raw.PriorityClassName != nil && (effective.PriorityClassName == nil || raw.GetPriorityClassName() != effective.GetPriorityClassName()) {
		return fmt.Errorf("%w: priority_class_name", ErrSchedulingConflict)
	}
	return nil
}
func ValidateCreateSpec(s *CreateSpec, accepted bool) error {
	if s == nil || hasUnknown(s.ProtoReflect()) || s.InstanceKind != "container" || (s.ComputeType != "cpu" && s.ComputeType != "gpu") || s.Name == "" || s.ImageId == "" || (s.ImageScope != "tenant" && s.ImageScope != "platform") || s.PresetId == "" || s.Cpu == "" || s.Memory == "" || s.ClusterId == "" || s.Namespace == "" {
		return ErrInvalidBusiness
	}
	if err := ValidateSchedulingOptions(s.Scheduling, true); err != nil {
		return err
	}
	if s.ComputeType == "cpu" {
		if s.GpuExecution != nil || s.GpuDisplay != nil || s.GpuSelection != nil {
			return ErrInvalidBusiness
		}
		return nil
	}
	if !accepted {
		if s.GpuSelection == nil {
			return ErrInvalidBusiness
		}
		return nil
	}
	if s.GpuSelection != nil || s.GpuDisplay == nil || s.GpuDisplay.ProfileId == "" || s.GpuDisplay.ProfileVersion == 0 {
		return ErrInvalidBusiness
	}
	if err := ValidateGpuExecution(s.GpuExecution); err != nil {
		return err
	}
	g, d := s.GpuExecution, s.GpuDisplay
	cores, _ := strconv.ParseInt(g.Resources["volcano.sh/vgpu-cores"], 10, 32)
	if d.ModelKey != g.NodeSelector["accelerator.ani.io/model-key"] {
		return ErrInvalidBusiness
	}
	switch d.Mode {
	case acc.SupplyMode_SHARED_FIXED:
		if g.Resources["volcano.sh/vgpu-number"] != "1" || cores >= 100 {
			return ErrInvalidBusiness
		}
	case acc.SupplyMode_WHOLE_EXCLUSIVE:
		if cores != 100 {
			return ErrInvalidBusiness
		}
	default:
		return ErrInvalidBusiness
	}
	return nil
}

// CommandDigest covers the immutable command identity and explicit business fields.
// The GPU execution has its own fixed-field digest; display remains audit data.
func CommandDigest(c *CommandContext, s *CreateSpec) (string, error) {
	if c == nil || s == nil || hasUnknown(c.ProtoReflect()) || hasUnknown(s.ProtoReflect()) {
		return "", ErrInvalidBusiness
	}
	execution := ""
	if s.GpuExecution != nil {
		execution = s.GpuExecution.ExecutionDigest
	}
	var scheduling, display any
	if p := s.Scheduling; p != nil {
		var placement, priority any
		if p.PodPlacement != nil {
			placement = fmt.Sprint(int32(p.GetPodPlacement()))
		}
		if p.PriorityClassName != nil {
			priority = p.GetPriorityClassName()
		}
		scheduling = map[string]any{"pod_placement": placement, "priority_class_name": priority}
	}
	if d := s.GpuDisplay; d != nil {
		display = map[string]any{"profile_id": d.ProfileId, "profile_version": fmt.Sprint(d.ProfileVersion), "profile_name": d.ProfileName, "model_key": d.ModelKey, "mode": fmt.Sprint(int32(d.Mode))}
	}
	value := map[string]any{"tenant_id": c.TenantId, "resource_id": c.ResourceId, "operation_id": c.OperationId, "name": s.Name, "image_id": s.ImageId, "image_scope": s.ImageScope, "preset_id": s.PresetId, "image_reference": s.ImageReference, "cpu": s.Cpu, "memory": s.Memory, "cluster_id": s.ClusterId, "namespace": s.Namespace, "service_account": s.ServiceAccount, "storage_class": s.StorageClass, "workspace_size": s.WorkspaceSize, "shell": s.Shell, "uid": fmt.Sprint(s.Uid), "gid": fmt.Sprint(s.Gid), "instance_kind": s.InstanceKind, "compute_type": s.ComputeType, "scheduling": scheduling, "preset_name": s.PresetName, "preset_code": s.PresetCode, "gpu_display": display, "execution_digest": execution}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("resource-create-v1\n"), raw...))
	return hex.EncodeToString(sum[:]), nil
}
func DeleteCommandDigest(r *AcceptDeleteRequest) (string, error) {
	if r == nil || r.Context == nil || hasUnknown(r.ProtoReflect()) {
		return "", ErrInvalidBusiness
	}
	c := r.Context
	raw, err := json.Marshal(map[string]any{"tenant_id": c.TenantId, "resource_id": c.ResourceId, "operation_id": c.OperationId, "create_operation_id": r.CreateOperationId, "expected_version": fmt.Sprint(r.ExpectedVersion), "instance_kind": r.InstanceKind, "compute_type": r.ComputeType})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("resource-delete-v1\n"), raw...))
	return hex.EncodeToString(sum[:]), nil
}

// Reserved fields must not disappear when gRPC messages enter the JSON digest.
func hasUnknown(m protoreflect.Message) bool {
	if len(m.GetUnknown()) != 0 {
		return true
	}
	unknown := false
	m.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			if field.MapValue().Kind() == protoreflect.MessageKind {
				value.Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
					unknown = hasUnknown(v.Message())
					return !unknown
				})
			}
		} else if field.IsList() {
			if field.Kind() == protoreflect.MessageKind {
				list := value.List()
				for k := 0; k < list.Len(); k++ {
					if hasUnknown(list.Get(k).Message()) {
						unknown = true
						break
					}
				}
			}
		} else if field.Kind() == protoreflect.MessageKind {
			unknown = hasUnknown(value.Message())
		}
		return !unknown
	})
	return unknown
}
