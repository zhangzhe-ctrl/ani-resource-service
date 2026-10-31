# Standard tenant Namespace and ModelDev pull Secret

This contract replaces configurable Namespace prefixes and Namespace initialization
in the Resource owners participating in ModelDev dynamic tenant environments.

`resource_tenant_id` is an unchanged, canonical lowercase, nonzero UUID. The
Namespace name is exactly `tenant-<resource_tenant_id>`. Each owner GETs that
name on its configured cluster, checks a nonempty UID, `phase=Active`, no deletion
timestamp, and these two labels:

- `network.ani.io/tenant-id=<resource_tenant_id>`
- `network.ani.io/managed-by=ani-network-service`

No owner creates, patches, adopts or deletes the Namespace. Missing or foreign
objects are errors. Original execution reads and cleanup keep their original
name/UID and do not require Active; same-name replacement never grants authority.
The pure rules and public positive/negative vectors live in
`internal/tenantnamespace/`.

Network accepts the live identity outside its database transaction and inserts
or compares that exact identity inside the existing acceptance transaction.
Migration 0025 adds nullable `network_tenant_namespaces.namespace_uid` without
changing historical migrations. Runtime rejects NULL. During a maintenance
window, stop new identity admissions, apply the migration, run
`scripts/tenant-namespace-backfill` first without `-apply`, review its identities
and SHA256, then apply with `-reviewed-sha256`. It only updates exact existing
NULL rows in the configured cluster; existing UIDs and other clusters are never
overwritten. Verify zero NULL in the before/after output before starting the new
runtime. The tool does not stop admissions or deploy the service itself.

`network.namespace_prefix`, `ANI_NETWORK_NAMESPACE_PREFIX`,
`NETWORK_NAMESPACE_PREFIX` and Instance's former nonempty prefix configuration
are rejected. Namespace field 4 and its source name are reserved in conf.proto.

ModelDev's authenticated `ani-modeldev-service` caller config contains only
subject and cluster allowlist. Its requested tenant must equal the trusted
metadata, and the exact standard name, original UID and cluster must be verified
before Secret writes. Its legacy namespace/owner-label allowlists are rejected.
The tenant's existing image space and pull robot remain the sole registry
credential source; missing space or wrong authority fails without initializing
Harbor. Resource writes only the deterministic Secret; it never writes an SA.
Namespace network ownership labels are separate from Secret labels:
`ani.io/managed-by=ani-resource-service`, image tenant, space and generation.
Inference retains its existing static caller branch and configuration.
