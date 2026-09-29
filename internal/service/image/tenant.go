package service

import (
 "context"
 imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
 biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
 "google.golang.org/protobuf/types/known/timestamppb"
)

type TenantService struct {
 imagev1.UnimplementedTenantImageServiceServer
 lifecycle *biz.Lifecycle
 catalog *biz.Catalog
}
func NewTenantService(lifecycle *biz.Lifecycle,catalog *biz.Catalog)*TenantService{return &TenantService{lifecycle:lifecycle,catalog:catalog}}
func(s *TenantService)EnsureImageSpace(ctx context.Context,r *imagev1.EnsureImageSpaceRequest)(*imagev1.EnsureImageSpaceResponse,error){
 v,err:=s.lifecycle.EnsureImageSpace(ctx,biz.EnableSpace{TenantID:r.GetTenantId(),Slug:r.GetSlug(),IdempotencyKey:r.GetIdempotencyKey()});if err!=nil{return nil,rpcError(err)};return &imagev1.EnsureImageSpaceResponse{Space:wireSpace(v)},nil
}
func(s *TenantService)GetImageSpace(ctx context.Context,r *imagev1.GetImageSpaceRequest)(*imagev1.GetImageSpaceResponse,error){
 v,err:=s.lifecycle.GetImageSpace(ctx,r.GetTenantId());if err!=nil{return nil,rpcError(err)};return &imagev1.GetImageSpaceResponse{Space:wireSpace(v)},nil
}
func(s *TenantService)GetPublisherCredential(ctx context.Context,r *imagev1.GetPublisherCredentialRequest)(*imagev1.GetPublisherCredentialResponse,error){
 v,err:=s.lifecycle.GetPublisherCredential(ctx,r.GetTenantId());if err!=nil{return nil,rpcError(err)};return &imagev1.GetPublisherCredentialResponse{Credential:wireCredential(v)},nil
}
func(s *TenantService)IssuePublisherCredential(ctx context.Context,r *imagev1.IssuePublisherCredentialRequest)(*imagev1.IssuePublisherCredentialResponse,error){
 v,err:=s.lifecycle.IssuePublisherCredential(ctx,biz.IssueCredential{TenantID:r.GetTenantId(),IdempotencyKey:r.GetIdempotencyKey(),ExpectedVersion:r.GetExpectedVersion()});if err!=nil{return nil,rpcError(err)};defer clear(v.Secret);return &imagev1.IssuePublisherCredentialResponse{Credential:wireCredential(v.Credential),Secret:string(v.Secret),ReplayUntil:timestamppb.New(v.ReplayUntil)},nil
}
func(s *TenantService)ResetPublisherCredential(ctx context.Context,r *imagev1.ResetPublisherCredentialRequest)(*imagev1.ResetPublisherCredentialResponse,error){
 v,err:=s.lifecycle.ResetPublisherCredential(ctx,biz.ResetCredential{TenantID:r.GetTenantId(),IdempotencyKey:r.GetIdempotencyKey(),ExpectedVersion:r.GetExpectedVersion()});if err!=nil{return nil,rpcError(err)};defer clear(v.Secret);return &imagev1.ResetPublisherCredentialResponse{Credential:wireCredential(v.Credential),Secret:string(v.Secret),ReplayUntil:timestamppb.New(v.ReplayUntil)},nil
}
func(s *TenantService)DisablePublisherCredential(ctx context.Context,r *imagev1.DisablePublisherCredentialRequest)(*imagev1.DisablePublisherCredentialResponse,error){
 v,err:=s.lifecycle.DisablePublisherCredential(ctx,biz.DisableCredential{TenantID:r.GetTenantId(),IdempotencyKey:r.GetIdempotencyKey(),ExpectedVersion:r.GetExpectedVersion()});if err!=nil{return nil,rpcError(err)};return &imagev1.DisablePublisherCredentialResponse{Credential:wireCredential(v)},nil
}
func(s *TenantService)RegisterImage(ctx context.Context,r *imagev1.RegisterImageRequest)(*imagev1.RegisterImageResponse,error){
 m,err:=readMetadata(r.GetDisplayName(),r.GetDescription(),r.GetPurposes(),r.GetAccelerator());if err!=nil{return nil,rpcError(err)}
 v,err:=s.catalog.RegisterImage(ctx,biz.RegisterImage{TenantID:r.GetTenantId(),ImageReference:r.GetImageReference(),IdempotencyKey:r.GetIdempotencyKey(),Metadata:m});if err!=nil{return nil,rpcError(err)};return &imagev1.RegisterImageResponse{Image:wireRegistration(v)},nil
}
func(s *TenantService)GetImage(ctx context.Context,r *imagev1.GetImageRequest)(*imagev1.GetImageResponse,error){
 scope,err:=readScope(r.GetScope());if err!=nil{return nil,rpcError(err)};v,err:=s.catalog.GetImage(ctx,biz.ReadImage{TenantID:r.GetTenantId(),ImageID:r.GetImageId(),Scope:scope});if err!=nil{return nil,rpcError(err)};return &imagev1.GetImageResponse{Image:wireRegistration(v)},nil
}
func(s *TenantService)ListImages(ctx context.Context,r *imagev1.ListImagesRequest)(*imagev1.ListImagesResponse,error){
 scope,err:=readScope(r.GetScope());if err!=nil{return nil,rpcError(err)};p,a,err:=readDeclarations(r.GetPurposes(),r.GetAccelerator());if err!=nil{return nil,rpcError(err)}
 v,err:=s.catalog.ListImages(ctx,biz.ListImages{TenantID:r.GetTenantId(),Scope:scope,Filter:biz.Filter{Search:r.GetSearch(),Purposes:p,Accelerator:a,Limit:int(r.GetLimit())},Cursor:r.GetCursor()});if err!=nil{return nil,rpcError(err)}
 out:=&imagev1.ListImagesResponse{NextCursor:v.NextCursor,Items:make([]*imagev1.ImageRegistration,0,len(v.Items))};for _,item:=range v.Items{out.Items=append(out.Items,wireRegistration(item))};return out,nil
}
func(s *TenantService)UpdateImage(ctx context.Context,r *imagev1.UpdateImageRequest)(*imagev1.UpdateImageResponse,error){
 m,err:=readMetadata(r.GetDisplayName(),r.GetDescription(),r.GetPurposes(),r.GetAccelerator());if err!=nil{return nil,rpcError(err)}
 v,err:=s.catalog.UpdateImage(ctx,biz.UpdateImage{TenantID:r.GetTenantId(),ImageID:r.GetImageId(),IdempotencyKey:r.GetIdempotencyKey(),ExpectedVersion:r.GetExpectedVersion(),Metadata:m});if err!=nil{return nil,rpcError(err)};return &imagev1.UpdateImageResponse{Image:wireRegistration(v)},nil
}
func(s *TenantService)UnregisterImage(ctx context.Context,r *imagev1.UnregisterImageRequest)(*imagev1.UnregisterImageResponse,error){
 v,err:=s.catalog.UnregisterImage(ctx,biz.UnregisterImage{TenantID:r.GetTenantId(),ImageID:r.GetImageId(),ExpectedVersion:r.GetExpectedVersion(),IdempotencyKey:r.GetIdempotencyKey()});if err!=nil{return nil,rpcError(err)};return &imagev1.UnregisterImageResponse{Image:wireRegistration(v)},nil
}
