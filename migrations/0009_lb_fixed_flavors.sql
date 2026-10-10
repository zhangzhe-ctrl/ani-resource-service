-- Preserve all existing small resources and acceptance receipts.
ALTER TABLE network_load_balancers DROP CONSTRAINT network_load_balancers_flavor_check;
ALTER TABLE network_load_balancers ADD CONSTRAINT network_load_balancers_flavor_check
  CHECK (flavor IN ('small', 'medium', 'large'));
