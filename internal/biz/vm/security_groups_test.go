package vm

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type sgRepository struct {
	Repository
	checkpoints int
}

func (r *sgRepository) Checkpoint(context.Context, Operation, VM) error { r.checkpoints++; return nil }

type sgNative struct {
	NativePort
	err   error
	calls int
}

func (n *sgNative) SetSecurityGroups(context.Context, VM, []string) error { n.calls++; return n.err }

type sgNetwork struct {
	confirm                       int
	err                           error
	syncVersions, confirmVersions []int64
}

func (n *sgNetwork) ResolveSecurityGroups(context.Context, string, string, string, []string) ([]string, error) {
	return []string{"sg-new"}, n.err
}
func (n *sgNetwork) SyncSecurityGroupBinding(_ context.Context, _, _, _, _, _ string, version int64, _ []string) ([]string, error) {
	n.syncVersions = append(n.syncVersions, version)
	return []string{"sg-new"}, n.err
}
func (n *sgNetwork) ConfirmSecurityGroupBinding(_ context.Context, _, _, _ string, version int64, _ []string) error {
	n.confirmVersions = append(n.confirmVersions, version)
	n.confirm++
	return nil
}
func (n *sgNetwork) ReleaseSecurityGroupBinding(context.Context, string, string, string, int64) error {
	return nil
}
func TestSecurityGroupsVMWorkerKeepsOldCurrentUntilNativeVerification(t *testing.T) {
	v := mediaFixtureVM()
	old := "sg_11111111111111111111111111111111"
	selected := "sg_22222222222222222222222222222222"
	v.Spec.SecurityGroupIDs = []string{old}
	v.SecurityGroupIDs = []string{old}
	v.SecurityGroupsInitialized = true
	o := Operation{Action: "set-security-groups", Intent: []byte(`{"security_group_ids":["sg_22222222222222222222222222222222"]}`), State: "running"}
	repo := &sgRepository{}
	native := &sgNative{err: ErrDependency}
	network := &sgNetwork{}
	w := &Worker{repo: repo, native: native, securityGroups: network}
	if err := w.setSecurityGroups(context.Background(), &o, &v); !errors.Is(err, ErrDependency) {
		t.Fatal(err)
	}
	if network.confirm != 0 || !reflect.DeepEqual(v.SecurityGroupIDs, []string{old}) || o.Pending == nil {
		t.Fatal("incomplete native update released old binding")
	}
	native.err = nil
	if err := w.setSecurityGroups(context.Background(), &o, &v); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(network.syncVersions, []int64{v.Version, v.Version}) || !reflect.DeepEqual(network.confirmVersions, []int64{v.Version}) {
		t.Fatal("reference fence did not use the persisted VM version", network)
	}
	if network.confirm != 1 || !reflect.DeepEqual(v.SecurityGroupIDs, []string{selected}) || o.State != "succeeded" || o.Pending != nil || !reflect.DeepEqual(v.Spec.SecurityGroupIDs, []string{old}) {
		t.Fatal("retry lost immutable/current separation", v, o)
	}
}
