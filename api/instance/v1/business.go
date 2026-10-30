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
	if m == nil || !m.ProtoReflect().IsValid() || hasUnknown(m.ProtoReflect()) {
		return nil, ErrInvalidBusiness
	}
	if _, e := (protojson.MarshalOptions{}).Marshal(m); e != nil {
		return nil, e
	}
	var value map[string]any
	switch v := m.(type) {
	case *CreateInstanceData:
		if e := ValidateInstanceConfiguration(v); e != nil {
			return nil, e
		}
		value = map[string]any{"name": v.Name, "image_id": v.ImageId, "image_scope": v.ImageScope, "preset_id": v.PresetId, "scheduling": wireJSON(v.Scheduling)}
		workspace := "/workspace"
		if v.WorkspaceMountPath != nil {
			workspace = *v.WorkspaceMountPath
		}
		value["workspace_mount_path"] = workspace
		mounts := []any{}
		for _, x := range sortedInputMounts(v.VolumeMounts) {
			mounts = append(mounts, wireJSON(x))
		}
		value["volume_mounts"] = mounts
		value["network"] = wireJSON(v.Network)
		value["labels"] = nonNilMap(v.Labels)
		value["annotations"] = nonNilMap(v.Annotations)
		value["env"] = wireEnv(v.Env)
		if len(v.SecurityGroupIds) > 0 {
			value["security_group_ids"] = v.SecurityGroupIds
		}

	case *CreateSpec:
		if e := validateSpecConfiguration(v); e != nil {
			return nil, e
		}
		value = map[string]any{"name": v.Name, "image_id": v.ImageId, "image_scope": v.ImageScope, "preset_id": v.PresetId, "image_reference": v.ImageReference, "cpu": v.Cpu, "memory": v.Memory, "cluster_id": v.ClusterId, "namespace": v.Namespace, "service_account": v.ServiceAccount, "storage_class": v.StorageClass, "workspace_size": v.WorkspaceSize, "shell": v.Shell, "uid": strconv.FormatInt(v.Uid, 10), "gid": strconv.FormatInt(v.Gid, 10), "instance_kind": v.InstanceKind, "compute_type": v.ComputeType, "scheduling": wireJSON(v.Scheduling), "preset_name": v.PresetName, "preset_code": v.PresetCode, "gpu_execution": wireJSON(v.GpuExecution), "gpu_display": wireJSON(v.GpuDisplay), "gpu_selection": wireJSON(v.GpuSelection)}
		value["workspace_mount_path"] = v.WorkspaceMountPath
		mounts := []any{}
		for _, x := range sortedFrozenMounts(v.VolumeMounts) {
			mounts = append(mounts, wireJSON(x))
		}
		value["volume_mounts"] = mounts
		value["network"] = wireJSON(v.Network)
		value["labels"] = nonNilMap(v.Labels)
		value["annotations"] = nonNilMap(v.Annotations)
		value["env"] = wireEnv(v.Env)
		if len(v.SecurityGroupIds) > 0 {
			value["security_group_ids"] = v.SecurityGroupIds
		}

	default:
		return nil, errors.New("unsupported instance business message")
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if e := enc.Encode(value); e != nil {
		return nil, e
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}

// wireJSON is a closed field projection, including nested defaults. Adding
// descriptor fields must not rewrite an accepted current payload.
func wireJSON(m proto.Message) any {
	if m == nil || !m.ProtoReflect().IsValid() {
		return nil
	}
	switch v := m.(type) {
	case *SchedulingOptions:
		out := map[string]any{}
		if v.PodPlacement != nil {
			out["pod_placement"] = fixedEnum(int32(*v.PodPlacement), map[int32]string{0: "POD_PLACEMENT_UNSPECIFIED", 1: "NONE", 2: "COLOCATE_PREFERRED", 3: "SPREAD_PREFERRED"})
		}
		if v.PriorityClassName != nil {
			out["priority_class_name"] = *v.PriorityClassName
		}
		return out
	case *acc.GpuExecution:
		return map[string]any{"scheduler_name": v.SchedulerName, "queue_name": v.QueueName, "runtime_class_name": v.RuntimeClassName, "node_selector": nonNilMap(v.NodeSelector), "pod_annotations": nonNilMap(v.PodAnnotations), "resources": nonNilMap(v.Resources), "execution_digest": v.ExecutionDigest}
	case *acc.GpuDisplay:
		return map[string]any{"profile_id": v.ProfileId, "profile_version": strconv.FormatUint(v.ProfileVersion, 10), "profile_name": v.ProfileName, "model_key": v.ModelKey, "mode": supplyModeJSON(int32(v.Mode))}
	case *GpuPresetReference:
		return map[string]any{"profile_id": v.ProfileId, "profile_version": strconv.FormatUint(v.ProfileVersion, 10), "spec_digest": v.SpecDigest, "devices_per_instance": v.DevicesPerInstance, "cluster_id": v.ClusterId, "pool_id": v.PoolId, "mode": supplyModeJSON(int32(v.Mode)), "model_key": v.ModelKey, "memory_mib_per_device": strconv.FormatInt(v.MemoryMibPerDevice, 10), "core_limit_percent": v.CoreLimitPercent}
	case *VolumeMount:
		return map[string]any{"pvc_name": v.PvcName, "mount_path": v.MountPath, "read_only": v.ReadOnly}
	case *FrozenVolumeMount:
		return map[string]any{"cluster_id": v.ClusterId, "namespace": v.Namespace, "pvc_name": v.PvcName, "pvc_uid": v.PvcUid, "mount_path": v.MountPath, "read_only": v.ReadOnly, "ownership": v.Ownership}
	case *NetworkSelection:
		return map[string]any{"vpc_id": v.VpcId, "subnet_id": v.SubnetId}
	case *InstanceEnvVar:
		return map[string]any{"name": v.Name, "value": v.Value}
	default:
		panic("unsupported immutable instance field projection")
	}
}
func fixedEnum(number int32, names map[int32]string) any {
	if name, ok := names[number]; ok {
		return name
	}
	return number
}
func supplyModeJSON(number int32) any {
	return fixedEnum(number, map[int32]string{int32(acc.SupplyMode_SUPPLY_MODE_UNSPECIFIED): "SUPPLY_MODE_UNSPECIFIED", int32(acc.SupplyMode_WHOLE_EXCLUSIVE): "WHOLE_EXCLUSIVE", int32(acc.SupplyMode_SHARED_FIXED): "SHARED_FIXED"})
}
func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
func wireEnv(env []*InstanceEnvVar) []any {
	out := []any{}
	for _, v := range env {
		out = append(out, wireJSON(v))
	}
	return out
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	descriptor := b.ProtoReflect().Descriptor().Fields()
	for name := range fields {
		if descriptor.ByName(protoreflect.Name(name)) == nil && descriptor.ByJSONName(name) == nil {
			return nil, ErrInvalidBusiness
		}
	}
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
	if err := validateSpecConfiguration(s); err != nil {
		return err
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
	if err := validateSpecConfiguration(s); err != nil {
		return "", err
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
	domain := "resource-create\n"
	value["workspace_mount_path"] = s.WorkspaceMountPath
	mounts := []any{}
	for _, m := range sortedFrozenMounts(s.VolumeMounts) {
		mounts = append(mounts, wireJSON(m))
	}
	value["volume_mounts"] = mounts
	value["network"] = wireJSON(s.Network)
	value["labels"] = nonNilMap(s.Labels)
	value["annotations"] = nonNilMap(s.Annotations)
	value["env"] = wireEnv(s.Env)

	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(domain), raw...))
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
