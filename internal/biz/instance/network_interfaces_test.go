package instance

import (
	"context"
	"testing"
)

type manualNetworkFixture struct {
	NetworkPort
	kept []string
}

func (n *manualNetworkFixture) ReserveNetworkInterface(_ context.Context, _ Instance, _ Operation, i NetworkInterfaceIntent) (NetworkInterfaceChoice, error) {
	return NetworkInterfaceChoice{Mode: i.Mode, VNicID: i.VNicID, ProviderName: "target", ProviderUID: "original"}, nil
}
func (n *manualNetworkFixture) SyncNetworkInterfaces(context.Context, Instance, []string) error {
	return nil
}
func (n *manualNetworkFixture) ReleaseNetworkInterfaces(_ context.Context, _ Instance, keep []string) error {
	n.kept = append([]string{}, keep...)
	return nil
}
func TestManualNetworkContainerSaveDoesNotMutateNotebook(t *testing.T) {
	net := &manualNetworkFixture{}
	r := &sgWorkerRepository{}
	provider := &sgWorkerProvider{}
	i := Instance{ID: "i", Spec: Spec{Network: &NetworkSelection{}}, Desired: Running, RuntimeDesired: Running, Observed: Running, PodUID: "old-pod"}
	o := &Operation{ID: "original", Action: "set-network-interface", Version: 2, Execution: &Execution{SchemaVersion: 1, Phase: "saving_network_interface", NetworkInterfaceTarget: &NetworkInterfaceIntent{Slot: "primary", Mode: "manual", VNicID: "vnic_11111111111111111111111111111111"}}}
	w := &Worker{repo: r, provider: provider, network: net}
	if _, err := w.stepNetworkInterface(context.Background(), Work{Instance: i, Operation: o}); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 || r.instance.PodUID != "old-pod" || r.instance.RuntimeDesired != Running || !PrimaryNetworkInterface(r.instance).PendingRestart() || r.instance.ActiveOperation.State != "completed" {
		t.Fatal("save rolled runtime or failed to release operation", r.instance)
	}
}
func TestManualNetworkContainerCannotConfirmRunningOnly(t *testing.T) {
	c := NetworkInterfaceChoice{Mode: "manual", VNicID: "vnic_11111111111111111111111111111111"}
	i := Instance{NetworkInterfaces: []NetworkInterface{{Slot: "primary", Desired: c, Applied: c, RequestedRevision: 2, AppliedRevision: 2}}, Network: &NetworkRuntime{SnapshotRevision: 2, ConfirmedSnapshotRevision: 1, NetworkState: "attached"}}
	o := Observation{PodUID: "new-pod", PodReady: true}
	if confirmNetworkInterface(&i, o) == nil {
		t.Fatal("running without attachment confirmation was accepted")
	}
	i.Network.ConfirmedSnapshotRevision = 2
	if err := confirmNetworkInterface(&i, o); err != nil {
		t.Fatal(err)
	}
}
