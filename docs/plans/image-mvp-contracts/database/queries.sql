-- sqlc draft. Add the remaining named queries described in image-data.md during IMG-02.
-- SQL is explicit and tenant-bound; no generic "empty tenant means all" query.

-- name: GetTenantSpace :one
SELECT * FROM image.spaces WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id);

-- name: GetPlatformSpace :one
SELECT * FROM image.spaces WHERE owner_scope='platform' AND tenant_id IS NULL;

-- name: GetTenantRegistration :one
SELECT * FROM image.registrations
 WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id) AND image_id=sqlc.arg(image_id);

-- name: GetPlatformRegistration :one
SELECT * FROM image.registrations
 WHERE owner_scope='platform' AND tenant_id IS NULL AND image_id=sqlc.arg(image_id);

-- name: ListTenantRegistrations :many
SELECT * FROM image.registrations
 WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id) AND unregistered_at IS NULL
   AND (sqlc.arg(search_text)::text='' OR display_name ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\'
        OR repository ILIKE sqlc.arg(search_pattern)::text ESCAPE E'\\')
   AND (cardinality(sqlc.arg(purposes)::text[])=0 OR purposes && sqlc.arg(purposes)::text[])
   AND (sqlc.arg(accelerator)::text='' OR accelerator=sqlc.arg(accelerator))
   AND (NOT sqlc.arg(has_cursor)::boolean OR (created_at,image_id) < (sqlc.arg(after_created_at),sqlc.arg(after_image_id)))
 ORDER BY created_at DESC,image_id DESC LIMIT sqlc.arg(fetch_limit);

-- name: UpdateTenantMetadata :one
UPDATE image.registrations SET display_name=sqlc.arg(display_name),description=sqlc.arg(description),
 purposes=sqlc.arg(purposes),accelerator=sqlc.arg(accelerator),updated_by=sqlc.arg(actor),
 updated_at=sqlc.arg(now_at),version=version+1
 WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id) AND image_id=sqlc.arg(image_id)
 AND unregistered_at IS NULL AND version=sqlc.arg(expected_version)
RETURNING *;

-- name: UnregisterTenantRegistration :one
UPDATE image.registrations SET unregistered_at=sqlc.arg(now_at),updated_at=sqlc.arg(now_at),
 updated_by=sqlc.arg(actor),version=version+1
 WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id) AND image_id=sqlc.arg(image_id)
 AND unregistered_at IS NULL AND version=sqlc.arg(expected_version)
RETURNING *;

-- name: GetTenantCommand :one
SELECT * FROM image.commands WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id)
 AND space_id=sqlc.arg(space_id) AND idempotency_key=sqlc.arg(idempotency_key);

-- name: GetTenantPullCredential :one
SELECT * FROM image.credentials WHERE owner_scope='tenant' AND tenant_id=sqlc.arg(tenant_id)
 AND space_id=sqlc.arg(space_id) AND purpose='pull';
