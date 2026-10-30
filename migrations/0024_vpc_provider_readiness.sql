-- VPC Provider readiness must remain independent of optional base connectivity.
-- Existing rows require a real fenced observation after migration.
ALTER TABLE network_provider_bindings
 ADD COLUMN provider_ready boolean NOT NULL DEFAULT false,
 ADD COLUMN provider_observed_at timestamptz,
 ADD COLUMN provider_observed_uid text NOT NULL DEFAULT '',
 ADD COLUMN provider_generation bigint NOT NULL DEFAULT 0,
 ADD COLUMN provider_observed_generation bigint NOT NULL DEFAULT 0;
