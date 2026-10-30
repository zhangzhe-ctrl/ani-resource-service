package instance

import (
	"errors"
	"github.com/zhangzhe-ctrl/ani-resource-service/pkg/instanceconfig"
)

// ValidateConfiguration guards the current frozen configuration and decoded snapshots.
func ValidateConfiguration(s Spec) error {
	if ValidateSecurityGroupIDs(s.SecurityGroupIDs) != nil || (len(s.SecurityGroupIDs) > 0 && s.Network == nil) {
		return ErrInvalid
	}
	paths := make([]string, 0, len(s.VolumeMounts))
	for _, m := range s.VolumeMounts {
		if m.ClusterID != s.ClusterID || m.Namespace != s.Namespace || m.PVCUID == "" || m.PVCName == "" || m.Ownership != "borrowed" {
			return ErrInvalid
		}
		paths = append(paths, m.MountPath)
	}
	names, values := make([]string, 0, len(s.Env)), make([]string, 0, len(s.Env))
	for _, v := range s.Env {
		names = append(names, v.Name)
		values = append(values, v.Value)
	}
	if s.Network != nil && (s.Network.VPCID == "" || s.Network.SubnetID == "") {
		return ErrInvalid
	}
	err := instanceconfig.Validate(s.WorkspaceMountPath, paths, s.Labels, s.Annotations, names, values)
	if errors.Is(err, instanceconfig.ErrReserved) {
		return ErrReservedConfig
	}
	if err != nil {
		return ErrInvalid
	}
	return nil
}
