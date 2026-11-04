package sandbox

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	compute "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/compute"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/protobuf/proto"
)

// AcceptSandboxClone accepts only a fully authorized, frozen CreateSpec from
// Governance. It replays the exact accepted command before consulting today's
// namespace, image, runtime or storage policy.
func (s *Service) AcceptSandboxClone(ctx context.Context, r *pb.AcceptSandboxCloneRequest) (*pb.AcceptSandboxCloneResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || r.Context == nil || r.Spec == nil || r.Spec.Workspace == nil || r.Spec.ContractVersion != 1 || !biz.UUID(r.Context.ResourceId) || !biz.UUID(r.Context.OperationId) || !biz.UUID(r.SourceSandboxId) || !biz.UUID(r.CheckpointId) || r.SourceSandboxId == r.Context.ResourceId || r.ExpectedVersion < 1 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.Context.TenantId); err != nil {
		return nil, rpcError(err)
	}
	decoded, err := hex.DecodeString(r.CreateIntentDigest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != r.CreateIntentDigest {
		return nil, rpcError(biz.ErrInvalid)
	}
	normalizedEgress, egressErr := pb.NormalizeEgressIntent(r.Spec.EgressIntent)
	if egressErr != nil || !proto.Equal(normalizedEgress, r.Spec.EgressIntent) {
		return nil, rpcError(biz.ErrInvalid)
	}
	digest, digestErr := pb.CloneCommandDigest(r.Context, r.SourceSandboxId, r.CheckpointId, r.ExpectedVersion, r.Spec, r.CreateIntentDigest)
	selDigest, selErr := selectionDigest(compute.Selection{PresetID: r.Spec.PresetId, Code: r.Spec.PresetCode, Name: r.Spec.PresetName, ComputeType: r.Spec.ComputeType, CPUMillis: r.Spec.CpuMillis, MemoryMiB: r.Spec.MemoryMib})
	specDigest, specErr := frozenSpecDigest(r.Spec)
	imageDigest, imageOK := biz.ImageDigest(r.Spec.ImageReference)
	if !imageOK || imageDigest != r.Spec.ImageDigest || r.Spec.NamespaceUid == "" || strings.TrimSpace(r.Spec.ImageReference) != r.Spec.ImageReference || strings.TrimSpace(r.Spec.Name) == "" || len([]rune(r.Spec.Name)) > 80 || !biz.UUID(r.Spec.PresetId) || !biz.UUID(r.Spec.ClusterId) || r.Spec.CpuMillis <= 0 || r.Spec.MemoryMib <= 0 || digestErr != nil || digest != r.Context.RequestDigest || selErr != nil || r.Spec.SelectionDigest != selDigest || specErr != nil || r.Spec.SpecDigest != specDigest {
		return nil, rpcError(biz.ErrInvalid)
	}
	cloneRepo, ok := s.repo.(biz.CheckpointCloneRepository)
	if !ok {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	createOp := uuid.NewSHA1(uuid.NameSpaceOID, []byte("sandbox-clone-create-v1\n"+r.Context.TenantId+"\n"+r.Context.OperationId)).String()
	cmd := biz.CheckpointCloneCommand{
		Transfer:              biz.CheckpointTransferCommand{TenantID: r.Context.TenantId, TransferID: r.Context.OperationId, OperationID: r.Context.OperationId, CheckpointID: r.CheckpointId, SourceSandboxID: r.SourceSandboxId, TargetSandboxID: r.Context.ResourceId, Kind: "clone", IdempotencyKey: r.Context.OperationId, ExpectedVersion: 1},
		Sandbox:               biz.Create{TenantID: r.Context.TenantId, ID: r.Context.ResourceId, OperationID: createOp, RequestDigest: r.Context.RequestDigest, IntentDigest: r.CreateIntentDigest, Spec: specValue(r.Spec)},
		SourceExpectedVersion: r.ExpectedVersion,
	}
	accepted, err := cloneRepo.LookupCheckpointClone(ctx, cmd)
	if err == nil {
		return &pb.AcceptSandboxCloneResponse{Ack: ackProto(biz.ACK{OperationID: accepted.OperationID, ResourceID: accepted.TargetSandboxID, Accepted: true, Replayed: true})}, nil
	}
	if !errors.Is(err, biz.ErrNotFound) {
		return nil, rpcError(err)
	}
	// Preserve previously accepted clones before checking current capability.
	if err := requireEnforceableEgress(normalizedEgress); err != nil {
		return nil, err
	}
	namespaceUID, err := s.config.NamespaceResolver.ResolveSandboxNamespaceUID(ctx, r.Context.TenantId)
	if err != nil {
		return nil, rpcError(err)
	}
	if namespaceUID == "" || namespaceUID != r.Spec.NamespaceUid {
		return nil, rpcError(biz.ErrOwnership)
	}
	var imageRef string
	if r.Spec.ImageId != "" || r.Spec.ImageScope != "" {
		imageRef, err = s.resolveImageID(ctx, r.Context.TenantId, r.Spec.ImageId, r.Spec.ImageScope)
	} else {
		imageRef, err = s.resolveImage(ctx, r.Context.TenantId, r.Spec.ImageReference)
	}
	if err != nil {
		return nil, rpcError(err)
	}
	storageClass, err := s.storageClass(ctx, r.Context.TenantId)
	if err != nil {
		return nil, rpcError(err)
	}
	if err = s.validateRuntimeClass(ctx, r.Spec.RuntimeClass); err != nil {
		return nil, rpcError(err)
	}
	namespace, err := s.namespace(r.Context.TenantId)
	if err != nil || imageRef != r.Spec.ImageReference || r.Spec.ClusterId != s.config.ClusterID || r.Spec.Namespace != namespace || r.Spec.Workspace.MountPath != "/workspace" || !validWorkspaceSize(r.Spec.Workspace.Size) || r.Spec.Workspace.StorageClass != storageClass || r.Spec.Workspace.ReclaimPolicy != "Delete" {
		return nil, rpcError(biz.ErrInvalid)
	}
	if s.config.CheckpointTransferPreflight == nil {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	if err = s.config.CheckpointTransferPreflight.ValidateCheckpointTransferStorage(ctx, r.Spec.Workspace.StorageClass); err != nil {
		if errors.Is(err, biz.ErrCheckpointWFFCUnsupported) {
			rejector, ok := s.repo.(biz.CheckpointCloneRejector)
			if !ok {
				return nil, rpcError(biz.ErrDependencyUnavailable)
			}
			winner, rejectErr := rejector.RejectCheckpointCloneWFFC(ctx, cmd)
			if rejectErr != nil {
				return nil, rpcError(rejectErr)
			}
			return &pb.AcceptSandboxCloneResponse{Ack: ackProto(biz.ACK{OperationID: winner.OperationID, ResourceID: winner.TargetSandboxID, Accepted: true, Replayed: true})}, nil
		}
		// A transient preflight failure is not a durable rejection.
		if replay, lookupErr := cloneRepo.LookupCheckpointClone(ctx, cmd); lookupErr == nil {
			return &pb.AcceptSandboxCloneResponse{Ack: ackProto(biz.ACK{OperationID: replay.OperationID, ResourceID: replay.TargetSandboxID, Accepted: true, Replayed: true})}, nil
		} else if !errors.Is(lookupErr, biz.ErrNotFound) {
			return nil, rpcError(lookupErr)
		}
		return nil, rpcError(err)
	}
	accepted, err = cloneRepo.AcceptCheckpointClone(ctx, cmd)
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.AcceptSandboxCloneResponse{Ack: ackProto(biz.ACK{OperationID: accepted.OperationID, ResourceID: accepted.TargetSandboxID, Accepted: true, Replayed: accepted.Replayed})}, nil
}

func (s *Service) RestoreSandboxCheckpoint(ctx context.Context, r *pb.RestoreSandboxCheckpointRequest) (*pb.RestoreSandboxCheckpointResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) || !biz.UUID(r.CheckpointId) || r.ExpectedVersion < 1 || strings.TrimSpace(r.IdempotencyKey) == "" || len([]rune(r.IdempotencyKey)) > 256 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	repo, ok := s.repo.(biz.CheckpointTransferRepository)
	if !ok {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	reader, ok := s.repo.(biz.CheckpointTransferReplayReader)
	if !ok {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	command := biz.CheckpointTransferCommand{TenantID: r.TenantId, TransferID: uuid.NewString(), OperationID: uuid.NewString(), CheckpointID: r.CheckpointId, SourceSandboxID: r.SandboxId, TargetSandboxID: r.SandboxId, Kind: "restore", IdempotencyKey: r.IdempotencyKey, ExpectedVersion: r.ExpectedVersion}
	transfer, err := reader.LookupCheckpointTransfer(ctx, command)
	if err == nil {
		return &pb.RestoreSandboxCheckpointResponse{Ack: ackProto(biz.ACK{OperationID: transfer.OperationID, ResourceID: r.SandboxId, Accepted: true, Replayed: true})}, nil
	}
	if !errors.Is(err, biz.ErrNotFound) {
		return nil, rpcError(err)
	}
	target, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	if s.config.CheckpointTransferPreflight == nil {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	if err = s.config.CheckpointTransferPreflight.ValidateCheckpointTransferStorage(ctx, target.Snapshot.Workspace.StorageClass); err != nil {
		if errors.Is(err, biz.ErrCheckpointWFFCUnsupported) {
			rejector, ok := s.repo.(biz.CheckpointTransferRejector)
			if !ok {
				return nil, rpcError(biz.ErrDependencyUnavailable)
			}
			winner, rejectErr := rejector.RejectCheckpointTransferWFFC(ctx, command)
			if rejectErr != nil {
				return nil, rpcError(rejectErr)
			}
			return &pb.RestoreSandboxCheckpointResponse{Ack: ackProto(biz.ACK{OperationID: winner.OperationID, ResourceID: r.SandboxId, Accepted: true, Replayed: true})}, nil
		}
		if replay, lookupErr := reader.LookupCheckpointTransfer(ctx, command); lookupErr == nil {
			return &pb.RestoreSandboxCheckpointResponse{Ack: ackProto(biz.ACK{OperationID: replay.OperationID, ResourceID: r.SandboxId, Accepted: true, Replayed: true})}, nil
		} else if !errors.Is(lookupErr, biz.ErrNotFound) {
			return nil, rpcError(lookupErr)
		}
		return nil, rpcError(err)
	}
	transfer, err = repo.AcceptCheckpointTransfer(ctx, command)
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.RestoreSandboxCheckpointResponse{Ack: ackProto(biz.ACK{OperationID: transfer.OperationID, ResourceID: r.SandboxId, Accepted: true, Replayed: transfer.Replayed})}, nil
}
