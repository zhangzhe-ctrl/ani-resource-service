-- Resource persists a clone rejection before returning its dedicated receipt.
ALTER TABLE sandbox.checkpoint_transfer_rejections
 DROP CONSTRAINT checkpoint_transfer_rejections_reason_check;
ALTER TABLE sandbox.checkpoint_transfer_rejections
 ADD CONSTRAINT checkpoint_transfer_rejections_reason_check CHECK (
  reason = 'SANDBOX_TRANSFER_WFFC_UNSUPPORTED' OR
  (kind = 'clone' AND reason IN ('SANDBOX_CLONE_REJECTED_NOT_FOUND', 'SANDBOX_CLONE_REJECTED_VERSION', 'SANDBOX_CLONE_REJECTED_CONFLICT'))
 );
