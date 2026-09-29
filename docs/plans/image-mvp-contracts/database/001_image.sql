-- DESIGN INPUT: not applied or PostgreSQL-validated in this document session.
-- Move to migrations/image/001_image.sql during IMG-02.
-- Execute only with the dedicated Image owner. No RLS and no Network changes.
CREATE SCHEMA IF NOT EXISTS image;
REVOKE ALL ON SCHEMA image FROM PUBLIC;

CREATE TABLE image.schema_version (
    version integer PRIMARY KEY CHECK (version > 0),
    checksum char(64) NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE image.spaces (
    space_id uuid PRIMARY KEY,
    owner_scope text NOT NULL CHECK (owner_scope IN ('tenant','platform')),
    tenant_id uuid,
    installation_id uuid NOT NULL,
    registry_authority text NOT NULL CHECK (char_length(registry_authority) BETWEEN 1 AND 255),
    project_name text NOT NULL CHECK (char_length(project_name) BETWEEN 1 AND 48),
    harbor_project_id bigint CHECK (harbor_project_id > 0),
    state text NOT NULL DEFAULT 'provisioning' CHECK (state IN ('provisioning','available','blocked')),
    reason text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT image_space_scope CHECK (
      (owner_scope='tenant' AND tenant_id IS NOT NULL AND tenant_id <> '00000000-0000-0000-0000-000000000000')
      OR (owner_scope='platform' AND tenant_id IS NULL)),
    CONSTRAINT image_space_available CHECK (state <> 'available' OR harbor_project_id IS NOT NULL),
    UNIQUE (space_id, owner_scope),
    UNIQUE (space_id, tenant_id),
    UNIQUE (registry_authority, project_name)
);
CREATE UNIQUE INDEX image_space_one_per_tenant ON image.spaces (tenant_id) WHERE owner_scope='tenant';
CREATE UNIQUE INDEX image_space_one_platform ON image.spaces (owner_scope) WHERE owner_scope='platform';
CREATE UNIQUE INDEX image_space_harbor_id ON image.spaces (registry_authority, harbor_project_id) WHERE harbor_project_id IS NOT NULL;

CREATE TABLE image.credentials (
    space_id uuid NOT NULL,
    owner_scope text NOT NULL CHECK (owner_scope IN ('tenant','platform')),
    tenant_id uuid,
    purpose text NOT NULL CHECK (purpose IN ('publisher','pull')),
    state text NOT NULL CHECK (state IN ('issuing','active','disabled','blocked')),
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    robot_id bigint CHECK (robot_id > 0),
    robot_name text,
    username text,
    expires_at timestamptz,
    never_expires boolean NOT NULL DEFAULT false,
    secret_ciphertext bytea,
    secret_key_id text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (space_id,purpose),
    FOREIGN KEY (space_id,owner_scope) REFERENCES image.spaces(space_id,owner_scope),
    FOREIGN KEY (space_id,tenant_id) REFERENCES image.spaces(space_id,tenant_id),
    CONSTRAINT image_credential_scope CHECK (
      (owner_scope='tenant' AND tenant_id IS NOT NULL AND tenant_id <> '00000000-0000-0000-0000-000000000000')
      OR (owner_scope='platform' AND tenant_id IS NULL AND purpose='publisher')),
    CONSTRAINT image_credential_cipher_pair CHECK ((secret_ciphertext IS NULL) = (secret_key_id IS NULL)),
    CONSTRAINT image_publisher_no_longterm_secret CHECK (purpose <> 'publisher' OR secret_ciphertext IS NULL),
    CONSTRAINT image_credential_active CHECK (state <> 'active' OR
      (generation > 0 AND robot_id IS NOT NULL AND username IS NOT NULL AND robot_name IS NOT NULL
       AND ((never_expires AND expires_at IS NULL) OR (NOT never_expires AND expires_at IS NOT NULL))
       AND (purpose <> 'pull' OR secret_ciphertext IS NOT NULL)))
);
CREATE INDEX image_credential_tenant ON image.credentials(tenant_id,purpose) WHERE owner_scope='tenant';

CREATE TABLE image.registrations (
    image_id text PRIMARY KEY CHECK (image_id ~ '^img_[0-9a-f]{32}$'),
    space_id uuid NOT NULL,
    owner_scope text NOT NULL CHECK (owner_scope IN ('tenant','platform')),
    tenant_id uuid,
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 100),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    repository text NOT NULL CHECK (char_length(repository) BETWEEN 1 AND 128
      AND repository ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'),
    source_reference text NOT NULL CHECK (char_length(source_reference) BETWEEN 1 AND 512),
    digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    media_type text NOT NULL,
    platforms jsonb NOT NULL CHECK (jsonb_typeof(platforms)='array' AND jsonb_array_length(platforms) BETWEEN 1 AND 32),
    purposes text[] NOT NULL CHECK (cardinality(purposes) BETWEEN 1 AND 5
      AND purposes <@ ARRAY['container','development','inference','finetuning','training']::text[]
      AND array_position(purposes,NULL) IS NULL),
    accelerator text NOT NULL DEFAULT 'undeclared' CHECK (accelerator IN ('undeclared','none','nvidia','amd','ascend','other')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by text NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 128),
    updated_by text NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 128),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    unregistered_at timestamptz,
    FOREIGN KEY (space_id,owner_scope) REFERENCES image.spaces(space_id,owner_scope),
    FOREIGN KEY (space_id,tenant_id) REFERENCES image.spaces(space_id,tenant_id),
    CONSTRAINT image_registration_scope CHECK (
      (owner_scope='tenant' AND tenant_id IS NOT NULL AND tenant_id <> '00000000-0000-0000-0000-000000000000')
      OR (owner_scope='platform' AND tenant_id IS NULL))
);
CREATE UNIQUE INDEX image_registration_active_digest ON image.registrations(space_id,repository,digest) WHERE unregistered_at IS NULL;
CREATE INDEX image_registration_tenant_page ON image.registrations(tenant_id,created_at DESC,image_id DESC) WHERE owner_scope='tenant' AND unregistered_at IS NULL;
CREATE INDEX image_registration_platform_page ON image.registrations(created_at DESC,image_id DESC) WHERE owner_scope='platform' AND unregistered_at IS NULL;

CREATE TABLE image.commands (
    command_id uuid PRIMARY KEY,
    space_id uuid NOT NULL,
    owner_scope text NOT NULL CHECK (owner_scope IN ('tenant','platform')),
    tenant_id uuid,
    idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
    kind text NOT NULL CHECK (kind IN ('enable_space','issue_publisher','reset_publisher','disable_publisher','register_image','update_image','unregister_image')),
    actor text NOT NULL CHECK (char_length(actor) BETWEEN 1 AND 128),
    fingerprint_version integer NOT NULL DEFAULT 1 CHECK (fingerprint_version=1),
    fingerprint char(64) NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    state text NOT NULL CHECK (state IN ('pending','running','retryable','blocked','succeeded','failed')),
    phase text NOT NULL DEFAULT 'reserved' CHECK (char_length(phase) BETWEEN 1 AND 64),
    candidate jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(candidate)='object'),
    result jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(result)='object'),
    secret_ciphertext bytea,
    secret_key_id text,
    secret_replay_until timestamptz,
    reason text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    UNIQUE (space_id,idempotency_key),
    FOREIGN KEY (space_id,owner_scope) REFERENCES image.spaces(space_id,owner_scope),
    FOREIGN KEY (space_id,tenant_id) REFERENCES image.spaces(space_id,tenant_id),
    CONSTRAINT image_command_scope CHECK (
      (owner_scope='tenant' AND tenant_id IS NOT NULL AND tenant_id <> '00000000-0000-0000-0000-000000000000')
      OR (owner_scope='platform' AND tenant_id IS NULL)),
    CONSTRAINT image_command_cipher_pair CHECK ((secret_ciphertext IS NULL) = (secret_key_id IS NULL)),
    CONSTRAINT image_command_completion CHECK (
      (state IN ('succeeded','failed') AND completed_at IS NOT NULL)
      OR (state IN ('pending','running','retryable','blocked') AND completed_at IS NULL))
);
CREATE UNIQUE INDEX image_command_single_external_write ON image.commands(space_id)
 WHERE kind IN ('enable_space','issue_publisher','reset_publisher','disable_publisher')
   AND state IN ('pending','running','retryable','blocked');
CREATE INDEX image_command_secret_expiry ON image.commands(secret_replay_until) WHERE secret_ciphertext IS NOT NULL;

-- Roles/grants and schema_version insertion are handled by the explicit migration runner.
-- Never execute ALTER TABLE ... ENABLE ROW LEVEL SECURITY here.
-- Runtime gets DML on four business tables, SELECT only on schema_version, no schema CREATE.
