package sandbox

import "time"

// PreviewExperiment is a state-transition input for a future durable adapter.
// The initial zero value is disabled: opening requires explicit operator enablement.
// This model is not wired to public RPCs or Kubernetes and carries no URL.
type PreviewExperiment struct {
	Enabled                                                              bool
	TenantID, SandboxID, PodUID                                          string
	Version                                                              int64
	Port                                                                 uint16
	Protocol                                                             string // http and tcp both use TCP transport
	Desired                                                              string // open or closed
	ServiceUID, CreateMutationID, SliceUID, SliceCreateMutationID, PodIP string
	Pending                                                              *PreviewExperimentMutation
	ExpiresAt                                                            time.Time
}
type PreviewExperimentMutation struct{ ID, Kind, TargetUID, PodIP string }
type PreviewExperimentObservation struct {
	// A service is attributable only after checking namespace UID, Pod UID,
	// sandbox/tenant labels and the exact pending mutation ID.
	Present, Attributable, EndpointReady bool
	ServiceUID                           string
	SlicePresent, SliceAttributable      bool
	SliceUID                             string
	AllSlicesAbsent                      bool // verified complete provider snapshot
}
type PreviewExperimentDecision struct {
	Desired, State, Reason string
	// Begin is an intent that must be journaled before sending; it is not a
	// command to call a Provider without a locked Sandbox transaction.
	Begin *PreviewExperimentMutation
	// ClearPending must be persisted together with the attributed receipt.
	ClearPending                                                  bool
	ServiceUID, CreateMutationID, SliceUID, SliceCreateMutationID string
}

// DecideExperimentalPreview never infers cancellation from absence after a
// dispatched create. Pause, deletion and expiry ask for cleanup even when the
// experimental feature has since been disabled. A future retry must use a
// UID precondition and its own durable attempt fence.
func DecideExperimentalPreview(p PreviewExperiment, o PreviewExperimentObservation, sandboxRunning bool, now time.Time, newMutationID string) (PreviewExperimentDecision, error) {
	if !UUID(p.TenantID) || !UUID(p.SandboxID) || p.PodUID == "" || p.Version < 1 || p.Port == 0 || (p.Protocol != "http" && p.Protocol != "tcp") || (p.Desired != "open" && p.Desired != "closed") || p.ExpiresAt.IsZero() || now.IsZero() {
		return PreviewExperimentDecision{}, ErrInvalid
	}
	if p.Pending != nil && (p.Pending.ID == "" || (p.Pending.Kind != "create" && p.Pending.Kind != "delete" && p.Pending.Kind != "create_slice" && p.Pending.Kind != "delete_slice") || (p.Pending.Kind == "create" && p.Pending.TargetUID != "") || (p.Pending.Kind != "create" && p.Pending.TargetUID == "") || (p.Pending.Kind == "create_slice" && p.Pending.PodIP == "")) {
		return PreviewExperimentDecision{}, ErrInvalid
	}
	if o.Present && (!o.Attributable || o.ServiceUID == "") {
		return PreviewExperimentDecision{}, ErrOwnership
	}
	if o.SlicePresent && (!o.SliceAttributable || o.SliceUID == "") {
		return PreviewExperimentDecision{}, ErrOwnership
	}
	if !p.Enabled && p.Desired == "open" && p.ServiceUID == "" && p.Pending == nil && sandboxRunning && now.Before(p.ExpiresAt) {
		return PreviewExperimentDecision{}, ErrUnsupported
	}
	desired := p.Desired
	if !sandboxRunning || !now.Before(p.ExpiresAt) || !p.Enabled {
		desired = "closed"
	}
	result := PreviewExperimentDecision{Desired: desired, ServiceUID: p.ServiceUID, CreateMutationID: p.CreateMutationID, SliceUID: p.SliceUID, SliceCreateMutationID: p.SliceCreateMutationID}
	if p.Pending != nil {
		switch p.Pending.Kind {
		case "create":
			if !o.Present {
				result.State = "unknown"
				result.Reason = "CREATE_OUTCOME_UNKNOWN"
				return result, nil
			}
			if p.ServiceUID != "" && p.ServiceUID != o.ServiceUID {
				return PreviewExperimentDecision{}, ErrOwnership
			}
			result.ServiceUID = o.ServiceUID
			result.CreateMutationID = p.Pending.ID
			result.ClearPending = true
			result.State = "opening"
			result.Reason = "CREATE_RECEIPT_OBSERVED"
			return result, nil
		case "delete":
			if p.ServiceUID != p.Pending.TargetUID || p.SliceUID != "" {
				return PreviewExperimentDecision{}, ErrOwnership
			}
			if !o.Present {
				if !o.AllSlicesAbsent {
					result.State, result.Reason = "unknown", "ENDPOINT_REVOCATION_UNVERIFIED"
					return result, nil
				}
				result.ClearPending = true
				result.ServiceUID = ""
				result.State = "closed"
				return result, nil
			}
			if o.ServiceUID != p.Pending.TargetUID {
				return PreviewExperimentDecision{}, ErrOwnership
			}
			result.State = "closing"
			result.Reason = "DELETE_PENDING"
			return result, nil
		case "create_slice":
			if !o.SlicePresent {
				result.State = "unknown"
				result.Reason = "SLICE_CREATE_OUTCOME_UNKNOWN"
				return result, nil
			}
			if p.SliceUID != "" && p.SliceUID != o.SliceUID {
				return PreviewExperimentDecision{}, ErrOwnership
			}
			result.SliceUID = o.SliceUID
			result.SliceCreateMutationID = p.Pending.ID
			result.ClearPending = true
			result.State = "opening"
			return result, nil
		case "delete_slice":
			if p.SliceUID != p.Pending.TargetUID {
				return PreviewExperimentDecision{}, ErrOwnership
			}
			if !o.SlicePresent {
				if !o.AllSlicesAbsent {
					result.State, result.Reason = "unknown", "ENDPOINT_REVOCATION_UNVERIFIED"
					return result, nil
				}
				result.SliceUID = ""
				result.ClearPending = true
				result.State = "closing"
				return result, nil
			}
			if o.SliceUID != p.SliceUID {
				return PreviewExperimentDecision{}, ErrOwnership
			}
			result.State = "closing"
			return result, nil
		}
	}
	if p.ServiceUID != "" && o.Present && o.ServiceUID != p.ServiceUID {
		return PreviewExperimentDecision{}, ErrOwnership
	}
	if desired == "closed" {
		if p.SliceUID != "" {
			if !o.SlicePresent {
				result.State = "unknown"
				result.Reason = "MISSING_SLICE_DELETE_RECEIPT"
				return result, nil
			}
			if o.SliceUID != p.SliceUID {
				return PreviewExperimentDecision{}, ErrOwnership
			}
			if newMutationID == "" {
				return PreviewExperimentDecision{}, ErrInvalid
			}
			result.State = "closing"
			result.Begin = &PreviewExperimentMutation{ID: newMutationID, Kind: "delete_slice", TargetUID: p.SliceUID}
			return result, nil
		}
		if p.ServiceUID == "" {
			if o.Present {
				return PreviewExperimentDecision{}, ErrOwnership
			}
			result.State = "closed"
			return result, nil
		}
		if !o.Present {
			result.State = "unknown"
			result.Reason = "MISSING_DELETE_RECEIPT"
			return result, nil
		}
		if newMutationID == "" {
			return PreviewExperimentDecision{}, ErrInvalid
		}
		result.State = "closing"
		result.Begin = &PreviewExperimentMutation{ID: newMutationID, Kind: "delete", TargetUID: p.ServiceUID}
		return result, nil
	}
	if p.ServiceUID == "" {
		if o.Present {
			return PreviewExperimentDecision{}, ErrOwnership
		}
		if newMutationID == "" {
			return PreviewExperimentDecision{}, ErrInvalid
		}
		result.State = "opening"
		result.Begin = &PreviewExperimentMutation{ID: newMutationID, Kind: "create"}
		return result, nil
	}
	if !o.Present {
		result.State = "unknown"
		result.Reason = "SERVICE_MISSING"
		return result, nil
	}
	if p.SliceUID == "" && p.PodIP != "" {
		if newMutationID == "" {
			return PreviewExperimentDecision{}, ErrInvalid
		}
		result.State = "opening"
		result.Begin = &PreviewExperimentMutation{ID: newMutationID, Kind: "create_slice", TargetUID: p.ServiceUID, PodIP: p.PodIP}
		return result, nil
	}
	if p.SliceUID != "" && (!o.SlicePresent || o.SliceUID != p.SliceUID) {
		return PreviewExperimentDecision{}, ErrOwnership
	}
	if p.SliceUID == "" || !o.EndpointReady {
		result.State = "opening"
		result.Reason = "ENDPOINT_NOT_READY"
		return result, nil
	}
	result.State = "ready"
	return result, nil
}

// ExperimentalPreviewOpen is a durable command. TTL is fixed at acceptance;
// retries use the same input digest rather than moving the expiration forward.
type ExperimentalPreviewOpen struct {
	TenantID, SandboxID, PreviewID, OperationID, IdempotencyKey string
	ExpectedVersion                                             int64
	Port                                                        uint16
	Protocol                                                    string
	TTL                                                         time.Duration
}
type ExperimentalPreviewRecord struct {
	TenantID, SandboxID, PreviewID, OpenOperationID                                                                 string
	OperationID                                                                                                     string
	Version                                                                                                         int64
	PodUID                                                                                                          string
	Port                                                                                                            uint16
	Protocol, Desired, State, Reason, ServiceUID, CreateMutationID, SliceUID, SliceCreateMutationID, PodIP, Address string
	Pending                                                                                                         *PreviewExperimentMutation
	ExpiresAt                                                                                                       time.Time
	Replayed                                                                                                        bool
}

type ExperimentalPreviewClose struct {
	TenantID, SandboxID, PreviewID, OperationID, IdempotencyKey string
	ExpectedVersion                                             int64
}

type ExperimentalPreviewOperation struct {
	TenantID, SandboxID, PreviewID, OperationID, Action, State, Reason string
	CreatedAt                                                          time.Time
	CompletedAt                                                        *time.Time
}
