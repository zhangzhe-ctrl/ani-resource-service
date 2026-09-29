-- name: GetTenantPublisherCredential :one
SELECT * FROM image.credentials WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id)
 AND space_id=sqlc.arg(space_id) AND purpose='publisher';

-- name: GetTenantPullCredential :one
SELECT * FROM image.credentials WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id)
 AND space_id=sqlc.arg(space_id) AND purpose='pull';

-- name: GetPlatformPublisherCredential :one
SELECT * FROM image.credentials WHERE owner_scope='platform' AND tenant_id IS NULL
 AND space_id=sqlc.arg(space_id) AND purpose='publisher';

-- name: PutTenantCandidateState :one
INSERT INTO image.credentials(space_id,owner_scope,tenant_id,purpose,state)
VALUES(sqlc.arg(space_id),'tenant',sqlc.arg(tenant_id),sqlc.arg(purpose),'issuing')
ON CONFLICT(space_id,purpose) DO UPDATE SET state='issuing',updated_at=clock_timestamp()
WHERE image.credentials.owner_scope='tenant' AND image.credentials.tenant_id=sqlc.arg(tenant_id)
 AND image.credentials.generation=0 AND image.credentials.state='issuing'
RETURNING *;

-- name: PutPlatformCandidateState :one
INSERT INTO image.credentials(space_id,owner_scope,tenant_id,purpose,state)
VALUES(sqlc.arg(space_id),'platform',NULL,'publisher','issuing')
ON CONFLICT(space_id,purpose) DO UPDATE SET state='issuing',updated_at=clock_timestamp()
WHERE image.credentials.owner_scope='platform' AND image.credentials.tenant_id IS NULL
 AND image.credentials.generation=0 AND image.credentials.state='issuing'
RETURNING *;

-- name: ActivateTenantCredential :one
UPDATE image.credentials SET state='active',generation=sqlc.arg(generation),version=version+1,
 robot_id=sqlc.arg(robot_id),robot_name=sqlc.arg(robot_name),username=sqlc.arg(username),
 expires_at=sqlc.narg(expires_at),never_expires=sqlc.arg(never_expires),
 secret_ciphertext=sqlc.narg(secret_ciphertext),secret_key_id=sqlc.narg(secret_key_id),updated_at=clock_timestamp()
WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id) AND space_id=sqlc.arg(space_id) AND purpose=sqlc.arg(purpose)
 AND version=sqlc.arg(expected_version) AND generation<sqlc.arg(generation)
RETURNING *;

-- name: ActivatePlatformCredential :one
UPDATE image.credentials SET state='active',generation=sqlc.arg(generation),version=version+1,
 robot_id=sqlc.arg(robot_id),robot_name=sqlc.arg(robot_name),username=sqlc.arg(username),
 expires_at=sqlc.narg(expires_at),never_expires=sqlc.arg(never_expires),updated_at=clock_timestamp()
WHERE owner_scope='platform' AND tenant_id IS NULL AND space_id=sqlc.arg(space_id) AND purpose='publisher'
 AND version=sqlc.arg(expected_version) AND generation<sqlc.arg(generation)
RETURNING *;

-- name: DisableTenantCredential :one
UPDATE image.credentials SET state='disabled',secret_ciphertext=NULL,secret_key_id=NULL,version=version+1,updated_at=clock_timestamp()
WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id) AND space_id=sqlc.arg(space_id) AND purpose=sqlc.arg(purpose)
 AND version=sqlc.arg(expected_version) AND generation=sqlc.arg(generation)
RETURNING *;

-- name: DisablePlatformCredential :one
UPDATE image.credentials SET state='disabled',version=version+1,updated_at=clock_timestamp()
WHERE owner_scope='platform' AND tenant_id IS NULL AND space_id=sqlc.arg(space_id) AND purpose='publisher'
 AND version=sqlc.arg(expected_version) AND generation=sqlc.arg(generation)
RETURNING *;
