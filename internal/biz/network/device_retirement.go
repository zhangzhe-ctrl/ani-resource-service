package biz

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

type DeviceRetirement struct {
	DeviceID, OperationID, State, Reason, ConfigUID string
	Version, ExpectedVersion, FenceVersion          int64
	PendingUnknown                                  bool
	CreatedAt, UpdatedAt                            time.Time
	CompletedAt                                     *time.Time
}
type DeviceRetirementIntent struct {
	DeviceID, IdempotencyKey string
	ExpectedVersion          int64
}
type LegacyDeviceRetirementRepository interface {
	RetireNetworkDevice(context.Context, DeviceRetirementIntent, Attribution) (DeviceRetirement, error)
	GetNetworkDeviceRetirement(context.Context, string) (DeviceRetirement, error)
}
type LegacyDeviceRetirementPlan struct {
	ConfigUID, ConfigRV, ManagedRaw, DesiredManagedRaw, MarkerValue string
	MarkerPresent                                                   bool
}
type LegacyDeviceRetirementWork struct {
	Retirement                              DeviceRetirement
	ClusterID, BindingID, DeviceName, Owner string
	Nodes                                   []NodeInterface
	PreManaged, Prepared                    bool
	Epoch, ExecutorEpoch                    int64
	Now, DrainAfter                         time.Time
	Plan                                    LegacyDeviceRetirementPlan
}
type LegacyDeviceRetirementObservation struct {
	Plan          LegacyDeviceRetirementPlan
	BlockedReason Reason
	Cleared       bool
	CollectedAt   time.Time
	Hash          string
}
type LegacyDeviceRetirementWorkRepository interface {
	ClaimLegacyDeviceRetirement(context.Context, string, time.Duration) (LegacyDeviceRetirementWork, bool, error)
	PrepareLegacyDeviceRetirement(context.Context, LegacyDeviceRetirementWork, LegacyDeviceRetirementObservation) (LegacyDeviceRetirementWork, error)
	AssertLegacyDeviceRetirement(context.Context, LegacyDeviceRetirementWork) error
	FinishLegacyDeviceRetirement(context.Context, LegacyDeviceRetirementWork, LegacyDeviceRetirementObservation, string, Reason, time.Duration) error
}
type LegacyDeviceRetirementProvider interface {
	InspectLegacyDeviceRetirement(context.Context, LegacyDeviceRetirementWork) (LegacyDeviceRetirementObservation, error)
	ClearLegacyDeviceRetirement(context.Context, LegacyDeviceRetirementWork) error
}

func (e *Egress) RetireNetworkDevice(ctx context.Context, i DeviceRetirementIntent) (DeviceRetirement, error) {
	if _, err := e.authorization.Platform(ctx); err != nil {
		return DeviceRetirement{}, err
	}
	return DeviceRetirement{}, Fail(InvalidArgument, "standalone device registration has been removed")
}
func (e *Egress) GetNetworkDeviceRetirement(ctx context.Context, id string) (DeviceRetirement, error) {
	if _, err := e.authorization.Platform(ctx); err != nil {
		return DeviceRetirement{}, err
	}
	if !validEgressID(id, "device") {
		return DeviceRetirement{}, Fail(ResourceNotFound, "device retirement not found")
	}
	r, ok := e.repository.(LegacyDeviceRetirementRepository)
	if !ok {
		return DeviceRetirement{}, Fail(DependencyUnavailable, "device retirement persistence unavailable")
	}
	return r.GetNetworkDeviceRetirement(ctx, id)
}

// The retired registration never resumes its old adoption worker. This separate
// stepper only removes its own proven ConfigMap effect after references drain.
type LegacyDeviceRetirementStepper struct {
	repository LegacyDeviceRetirementWorkRepository
	provider   LegacyDeviceRetirementProvider
	owner      string
	policy     WorkerPolicy
}

func NewLegacyDeviceRetirementStepper(r LegacyDeviceRetirementWorkRepository, p LegacyDeviceRetirementProvider, owner string, policy WorkerPolicy) (*LegacyDeviceRetirementStepper, error) {
	id, err := uuid.Parse(owner)
	if r == nil || p == nil || err != nil || id == uuid.Nil || policy.RequestTimeout <= 0 || policy.Lease < 3*policy.RequestTimeout || policy.ObserveEvery <= 0 {
		return nil, Fail(InvalidArgument, "invalid retirement worker dependencies")
	}
	return &LegacyDeviceRetirementStepper{r, p, id.String(), policy}, nil
}
func (s *LegacyDeviceRetirementStepper) Step(ctx context.Context) (worked bool, err error) {
	defer func() {
		if errors.Is(err, ErrLeaseLost) {
			err = nil
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, s.policy.Lease)
	defer cancel()
	w, found, err := s.repository.ClaimLegacyDeviceRetirement(ctx, s.owner, s.policy.Lease)
	if err != nil || !found {
		return found, err
	}
	inspect := func() (LegacyDeviceRetirementObservation, error) {
		call, cancel := context.WithTimeout(ctx, s.policy.RequestTimeout)
		defer cancel()
		return s.provider.InspectLegacyDeviceRetirement(call, w)
	}
	observation, inspectErr := inspect()
	finish := func(o LegacyDeviceRetirementObservation, state string, reason Reason) (bool, error) {
		return true, s.repository.FinishLegacyDeviceRetirement(ctx, w, o, state, reason, s.policy.ObserveEvery)
	}
	if inspectErr != nil {
		return finish(observation, "blocked", providerReason(inspectErr))
	}
	if observation.BlockedReason != "" {
		return finish(observation, "blocked", observation.BlockedReason)
	}
	if observation.Cleared {
		return finish(observation, "retired", "")
	}
	w, err = s.repository.PrepareLegacyDeviceRetirement(ctx, w, observation)
	if err != nil {
		return true, err
	}
	call, cancel := context.WithTimeout(ctx, s.policy.RequestTimeout)
	mutationErr := s.provider.ClearLegacyDeviceRetirement(call, w)
	cancel()
	if mutationErr != nil {
		return finish(observation, "closing", providerReason(mutationErr))
	}
	observation, inspectErr = inspect()
	if inspectErr != nil {
		return finish(observation, "closing", providerReason(inspectErr))
	}
	if observation.BlockedReason != "" {
		return finish(observation, "blocked", observation.BlockedReason)
	}
	if !observation.Cleared {
		return finish(observation, "closing", ProviderNotReady)
	}
	return finish(observation, "retired", "")
}
