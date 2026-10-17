package instance

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	quota "github.com/zhangzhe-ctrl/ani-governance/api/quota/gen/go/quota/service/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/grpc"
	"time"
)

type QuotaClient interface {
	ReportResourceReleased(context.Context, *quota.ReportResourceReleasedRequest, ...grpc.CallOption) (*quota.ReportResourceReleasedResponse, error)
}
type Quota struct {
	repo   *Postgres
	client QuotaClient
}

func NewQuota(repo *Postgres, client QuotaClient) (*Quota, error) {
	if repo == nil || client == nil {
		return nil, biz.ErrInvalid
	}
	return &Quota{repo, client}, nil
}
func (q *Quota) Report(ctx context.Context, i biz.Instance) error {
	if i.Desired != biz.Deleted || !i.ComputeCleaned || i.Pending != nil {
		return biz.ErrInvalid
	}
	if i.Spec.ComputeType == biz.TypeCPU {
		if !i.Spec.GPU.Empty() {
			return biz.ErrInvalid
		}
		return nil
	}
	if i.Spec.ComputeType != biz.TypeGPU {
		return biz.ErrInvalid
	}
	if i.ExternalComputeStarted && (i.ComputeSealAt.IsZero() || !releaseDigest.MatchString(i.PhysicalReleaseDigest)) {
		return biz.ErrInvalid
	}
	event := uuid.NewSHA1(uuid.NameSpaceOID, []byte("resource-release:"+i.TenantID+":"+i.ID+":"+i.CreateOperationID)).String()
	const reason = "RESOURCE_RELEASED"
	if _, e := q.repo.pool.Exec(ctx, "INSERT INTO instance.instance_quota_release_outbox(tenant_id,instance_id,event_id,original_create_operation_id,reason) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,instance_id) DO NOTHING", i.TenantID, i.ID, event, i.CreateOperationID, reason); e != nil {
		return e
	}
	var storedEvent, create, storedReason string
	var acknowledged bool
	if e := q.repo.pool.QueryRow(ctx, "SELECT event_id::text,original_create_operation_id::text,reason,acknowledged FROM instance.instance_quota_release_outbox WHERE tenant_id=$1 AND instance_id=$2", i.TenantID, i.ID).Scan(&storedEvent, &create, &storedReason, &acknowledged); e != nil {
		return e
	}
	if storedEvent != event || create != i.CreateOperationID || storedReason != reason {
		return biz.ErrConflict
	}
	if acknowledged {
		return nil
	}
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	reply, e := q.client.ReportResourceReleased(call, &quota.ReportResourceReleasedRequest{ReleaseEventId: event, CreateOperationId: create, ResourceId: i.ID, Reason: quota.ReleaseReason_RESOURCE_RELEASED})
	if e != nil {
		return e
	}
	if reply == nil || reply.ReleaseEventId != event || reply.CreateOperationId != create || reply.ResourceId != i.ID || (reply.Status != quota.ResourceReleaseStatus_APPLIED && reply.Status != quota.ResourceReleaseStatus_ALREADY_APPLIED) {
		return fmt.Errorf("invalid resource release receipt")
	}
	_, e = q.repo.pool.Exec(ctx, "UPDATE instance.instance_quota_release_outbox SET acknowledged=true WHERE tenant_id=$1 AND instance_id=$2 AND event_id=$3 AND original_create_operation_id=$4 AND reason=$5", i.TenantID, i.ID, event, create, reason)
	return e
}
