//go:build imageintegration

package data_test

import (
 "testing"
 biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
)
func TestStaleGenerationCannotActivateOrReplay(t *testing.T){
 f,r,cfg,ring,ctx:=lifecycleFixture(t);l:=lifecycle(t,f.Repo,r,ring,cfg,nil);tenant:=tenantFrom(t,ctx)
 s,err:=l.EnsureImageSpace(ctx,biz.EnableSpace{TenantID:tenant,Slug:"stale",IdempotencyKey:"stale-enable"});if err!=nil{t.Fatal(err)}
 issue:=biz.IssueCredential{TenantID:tenant,IdempotencyKey:"stale-issue",ExpectedVersion:0};first,err:=l.IssuePublisherCredential(ctx,issue);if err!=nil{t.Fatal(err)}
 oldCommand,err:=f.Repo.FindTenantCommand(ctx,tenant,s.ID,issue.IdempotencyKey);if err!=nil{t.Fatal(err)}
 next,err:=l.ResetPublisherCredential(ctx,biz.ResetCredential{TenantID:tenant,IdempotencyKey:"stale-reset",ExpectedVersion:first.Credential.Version});if err!=nil{t.Fatal(err)}
 if _,_,err=f.Repo.ActivateTenantCandidate(ctx,s,oldCommand,biz.EncryptedSecret{});biz.ReasonOf(err)!=biz.VersionConflict{t.Fatal("old command activated",err)}
 sets:=r.secretSets;if _,err=l.IssuePublisherCredential(ctx,issue);biz.ReasonOf(err)!=biz.CredentialDeliveryExpired{t.Fatal("old command delivered secret",err)}
 current,err:=l.GetPublisherCredential(ctx,tenant);if err!=nil||current.RobotID!=next.Credential.RobotID||current.Generation!=2||r.secretSets!=sets||r.secrets[current.RobotID]!=string(next.Secret){t.Fatal("stale command changed current credential",err)}
}
