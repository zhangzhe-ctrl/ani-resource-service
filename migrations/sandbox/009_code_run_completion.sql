-- Historical updated_at is not necessarily a completion time, so existing
-- results retain NULL until an actual completion timestamp is known.
ALTER TABLE sandbox.code_runs ADD COLUMN completed_at timestamptz;
