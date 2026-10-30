# Egress runtime prerequisites

The Network process registers the tenant and platform gRPC services, including
virtual switches and legacy device retirement, and runs their durable workers.
The independent default VPC egress service requires the identity guard below.
The process requires the
additional reads/writes in [network-rbac.yaml](network-rbac.yaml). Adapt its
ServiceAccount subject to the actual deployment; this template is not applied by
any test or build script. No actor or tenant metadata header grants egress access.
A trusted ingress must explicitly supply `biz.EgressCaller`; until that integration
is installed, egress RPCs deny access. Controlled test injection is not IAM proof.

## Manually prepared interfaces

Administrators prepare the interfaces and upstream VLAN connectivity on every
target node, then enter the interface name when creating a
[layer 2 network](../../docs/specs/kcn-v09-underlay-flow.md). Names such as `bond0`
are accepted as user input; KCN determines whether the actual interface can be
attached. Resource reserves the logical device slot, freezes Node UID membership,
and lets its durable worker merge only its owned Node group label before creating
the VlanNetwork. KCN owns interface configuration and takeover annotations.

Resource no longer deploys a host facts collector or requires a physical inventory
approval, and it does not write `kcn-config` or legacy device-adoption markers.
The shared ConfigMap and Node reads remain necessary for KCN observations and
VPC address admission; Node patch remains necessary for owned group labels.
Application ownership checks still protect other groups and device reservations.

KCN feedback represents the configuration steps that KCN publishes. It does not
prove upstream reachability, continuous link health, safe redrive after a stable
configuration error, or physical bridge removal. The existing product flow keeps
these boundaries explicit; this change does not add KCN retry or bridge teardown.
Live deployment and acceptance results belong in the execution ledger.

## Platform default VPC egress

`network.platform_default_vpc_egress` enables the independent platform service
only for the configured cluster and the exact `kcn-system/kcn-cluster` VPC UID:

```yaml
network:
  cluster_id: <configured-cluster-id>
  platform_default_vpc_egress:
    cluster_uid: <verified-kube-system-namespace-uid>
    namespace: kcn-system
    name: kcn-cluster
    uid: <verified-default-vpc-uid>
```

Without the guard the service is not registered. Invalid configured identity
fails startup; a changed live identity fails admission and reconciliation. This
service does not adopt native EIP/Snat objects, modify default VPC/Subnet identity
or `natOutgoing`, or create tenant ownership for the system VPC. Its public pool
must pass the existing qualification and exact topology checks; it has its own
durable address reservation and inverse cleanup lifecycle.

## Intranet pools

[Intranet pool prerequisites](intranet.md) covers the default VPC identity,
existing destination routing ranges, independent evidence and allocation gates.
