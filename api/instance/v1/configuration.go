package instancev1

import (
	"errors"
	"github.com/zhangzhe-ctrl/ani-resource-service/pkg/instanceconfig"
	"sort"
)

var ErrReservedConfig = instanceconfig.ErrReserved

func ValidateInstanceConfiguration(d *CreateInstanceData) error {
	if d == nil || hasUnknown(d.ProtoReflect()) {
		return ErrInvalidBusiness
	}
	workspace := "/workspace"
	if d.WorkspaceMountPath != nil {
		workspace = *d.WorkspaceMountPath
	}
	paths := make([]string, 0, len(d.VolumeMounts))
	for _, m := range d.VolumeMounts {
		if m == nil || m.PvcName == "" || len(m.PvcName) > 253 {
			return ErrInvalidBusiness
		}
		paths = append(paths, m.MountPath)
	}
	if ValidateSecurityGroupIDs(d.SecurityGroupIds) != nil || (len(d.SecurityGroupIds) > 0 && d.Network == nil) {
		return ErrInvalidBusiness
	}
	if d.Network != nil && (d.Network.VpcId == "" || d.Network.SubnetId == "") {
		return ErrInvalidBusiness
	}
	names, values := make([]string, 0, len(d.Env)), make([]string, 0, len(d.Env))
	for _, v := range d.Env {
		if v == nil {
			return ErrInvalidBusiness
		}
		names = append(names, v.Name)
		values = append(values, v.Value)
	}
	err := instanceconfig.Validate(workspace, paths, d.Labels, d.Annotations, names, values)
	if errors.Is(err, instanceconfig.ErrReserved) {
		return ErrReservedConfig
	}
	if err != nil {
		return ErrInvalidBusiness
	}
	return nil
}
func validateSpecConfiguration(s *CreateSpec) error {
	path := s.WorkspaceMountPath
	d := &CreateInstanceData{WorkspaceMountPath: &path, Labels: s.Labels, Annotations: s.Annotations, Env: s.Env, Network: s.Network, SecurityGroupIds: s.SecurityGroupIds}
	for _, m := range s.VolumeMounts {
		if m == nil || m.ClusterId != s.ClusterId || m.Namespace != s.Namespace || m.PvcUid == "" || m.Ownership != "borrowed" {
			return ErrInvalidBusiness
		}
		d.VolumeMounts = append(d.VolumeMounts, &VolumeMount{PvcName: m.PvcName, MountPath: m.MountPath, ReadOnly: m.ReadOnly})
	}
	return ValidateInstanceConfiguration(d)
}
func sortedInputMounts(m []*VolumeMount) []*VolumeMount {
	out := append([]*VolumeMount(nil), m...)
	sort.Slice(out, func(i, j int) bool { return out[i].MountPath < out[j].MountPath })
	return out
}
func sortedFrozenMounts(m []*FrozenVolumeMount) []*FrozenVolumeMount {
	out := append([]*FrozenVolumeMount(nil), m...)
	sort.Slice(out, func(i, j int) bool { return out[i].MountPath < out[j].MountPath })
	return out
}
