-- One configuration format replaces the development-only version selector.
-- Drain old writers and remove all container instances, including tombstones,
-- through the authorized environment cleanup before this explicit migration.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM instance.instances) THEN
  RAISE EXCEPTION 'container configuration cutover requires an empty instance ledger';
 END IF;
END $$;
ALTER TABLE instance.instances DROP COLUMN instance_contract_version;
