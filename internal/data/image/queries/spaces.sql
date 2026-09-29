-- name: LockTenantSpace :exec
SELECT pg_advisory_xact_lock(hashtextextended('image.tenant:' || sqlc.arg(tenant_id)::text, 0));

-- name: ReserveTenantSpace :one
INSERT INTO image.spaces(space_id,owner_scope,tenant_id,installation_id,registry_authority,project_name)
VALUES(sqlc.arg(space_id),'tenant',sqlc.arg(tenant_id),sqlc.arg(installation_id),sqlc.arg(registry_authority),sqlc.arg(project_name))
RETURNING *;

-- name: ReservePlatformSpace :one
INSERT INTO image.spaces(space_id,owner_scope,tenant_id,installation_id,registry_authority,project_name)
VALUES(sqlc.arg(space_id),'platform',NULL,sqlc.arg(installation_id),sqlc.arg(registry_authority),sqlc.arg(project_name))
RETURNING *;

-- name: GetTenantSpace :one
SELECT * FROM image.spaces WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id);

-- name: GetPlatformSpace :one
SELECT * FROM image.spaces WHERE owner_scope='platform' AND tenant_id IS NULL;

-- name: BindTenantProject :one
UPDATE image.spaces SET harbor_project_id=sqlc.arg(project_id),version=version+1,updated_at=clock_timestamp()
WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id) AND space_id=sqlc.arg(space_id)
 AND version=sqlc.arg(expected_version) AND (harbor_project_id IS NULL OR harbor_project_id=sqlc.arg(project_id))
RETURNING *;

-- name: BindPlatformProject :one
UPDATE image.spaces SET harbor_project_id=sqlc.arg(project_id),version=version+1,updated_at=clock_timestamp()
WHERE owner_scope='platform' AND tenant_id IS NULL AND space_id=sqlc.arg(space_id)
 AND version=sqlc.arg(expected_version) AND (harbor_project_id IS NULL OR harbor_project_id=sqlc.arg(project_id))
RETURNING *;

-- name: SetTenantSpaceAvailable :one
UPDATE image.spaces s SET state='available',reason='',version=s.version+1,updated_at=clock_timestamp()
WHERE s.owner_scope='tenant' AND s.tenant_id=sqlc.arg(tenant_id) AND s.space_id=sqlc.arg(space_id)
 AND s.version=sqlc.arg(expected_version) AND s.harbor_project_id IS NOT NULL
 AND EXISTS (SELECT 1 FROM image.credentials c WHERE c.owner_scope='tenant' AND c.tenant_id=sqlc.arg(tenant_id)
  AND c.space_id=s.space_id AND c.purpose='pull' AND c.state='active' AND (c.never_expires OR c.expires_at>clock_timestamp()))
RETURNING s.*;

-- name: SetPlatformSpaceAvailable :one
UPDATE image.spaces SET state='available',reason='',version=version+1,updated_at=clock_timestamp()
WHERE owner_scope='platform' AND tenant_id IS NULL AND space_id=sqlc.arg(space_id)
 AND version=sqlc.arg(expected_version) AND harbor_project_id IS NOT NULL
RETURNING *;

-- name: SetTenantSpaceReason :one
UPDATE image.spaces SET state=sqlc.arg(state),reason=sqlc.arg(reason),version=version+1,updated_at=clock_timestamp()
WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id) AND space_id=sqlc.arg(space_id) AND version=sqlc.arg(expected_version)
RETURNING *;

-- name: SetPlatformSpaceReason :one
UPDATE image.spaces SET state=sqlc.arg(state),reason=sqlc.arg(reason),version=version+1,updated_at=clock_timestamp()
WHERE owner_scope='platform' AND tenant_id IS NULL AND space_id=sqlc.arg(space_id) AND version=sqlc.arg(expected_version)
RETURNING *;

-- name: InspectSpaceForOperator :one
-- Local operator mode only; never used by tenant RPC paths.
SELECT * FROM image.spaces WHERE space_id=sqlc.arg(space_id);
