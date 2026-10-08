package biz

import "context"

// Shared project/robot recovery mechanics dispatch through explicit scope
// ports. A Lifecycle built for tenant RPCs has no platform repository at all.
func (l *Lifecycle) platformWrite(ctx context.Context, scope ImageScope, tenant string) error {
	if scope != PlatformImages || tenant != "" || l.platformRepo == nil {
		return Fail(PermissionDenied, "platform persistence unavailable")
	}
	_, err := RequirePlatform(ctx)
	return err
}
func (l *Lifecycle) findCommand(ctx context.Context, s Space, key string) (Command, error) {
	if s.Scope == TenantImages {
		return l.repo.FindTenantCommand(ctx, s.TenantID, s.ID, key)
	}
	if err := l.platformWrite(ctx, s.Scope, s.TenantID); err != nil {
		return Command{}, err
	}
	return l.platformRepo.FindPlatformCommand(ctx, s.ID, key)
}
func (l *Lifecycle) saveCommandPhase(ctx context.Context, c Command) (Command, error) {
	if c.Scope == TenantImages {
		return l.repo.SaveTenantCommandPhase(ctx, c)
	}
	if err := l.platformWrite(ctx, c.Scope, c.TenantID); err != nil {
		return Command{}, err
	}
	return l.platformRepo.SavePlatformCommandPhase(ctx, c)
}
func (l *Lifecycle) bindProject(ctx context.Context, s Space, id int64) (Space, error) {
	if s.Scope == TenantImages {
		return l.repo.BindTenantProject(ctx, s, id)
	}
	if err := l.platformWrite(ctx, s.Scope, s.TenantID); err != nil {
		return Space{}, err
	}
	return l.platformRepo.BindPlatformProject(ctx, s, id)
}
func (l *Lifecycle) blockSpace(ctx context.Context, s Space, reason Reason) (Space, error) {
	if s.Scope == TenantImages {
		return l.repo.BlockTenantSpace(ctx, s, reason)
	}
	if err := l.platformWrite(ctx, s.Scope, s.TenantID); err != nil {
		return Space{}, err
	}
	return l.platformRepo.BlockPlatformSpace(ctx, s, reason)
}
func (l *Lifecycle) publisher(ctx context.Context, s Space) (CredentialInfo, error) {
	if s.Scope == TenantImages {
		return l.repo.GetTenantPublisher(ctx, s.TenantID, s.ID)
	}
	if err := l.platformWrite(ctx, s.Scope, s.TenantID); err != nil {
		return CredentialInfo{}, err
	}
	return l.platformRepo.GetPlatformPublisher(ctx, s.ID)
}
func (l *Lifecycle) prepareStoredCandidate(ctx context.Context, c Command) (Command, error) {
	if c.Scope == TenantImages {
		return l.repo.PrepareTenantCandidate(ctx, c)
	}
	if err := l.platformWrite(ctx, c.Scope, c.TenantID); err != nil {
		return Command{}, err
	}
	if c.Candidate.Purpose != "publisher" {
		return Command{}, Fail(PermissionDenied, "platform publisher required")
	}
	return l.platformRepo.PreparePlatformCandidate(ctx, c)
}
func (l *Lifecycle) activateCandidate(ctx context.Context, s Space, c Command, pull EncryptedSecret) (Space, Command, error) {
	if s.Scope == TenantImages && c.Scope == TenantImages {
		return l.repo.ActivateTenantCandidate(ctx, s, c, pull)
	}
	if err := l.platformWrite(ctx, s.Scope, s.TenantID); err != nil {
		return Space{}, Command{}, err
	}
	if c.Scope != PlatformImages || c.TenantID != "" || c.SpaceID != s.ID || c.Candidate.Purpose != "publisher" || len(pull.Ciphertext) != 0 {
		return Space{}, Command{}, Fail(PermissionDenied, "invalid platform candidate")
	}
	return l.platformRepo.ActivatePlatformCandidate(ctx, s, c)
}
