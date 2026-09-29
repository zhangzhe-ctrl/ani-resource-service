package service

import (
 "time"
 imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
 biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
 "google.golang.org/protobuf/types/known/timestamppb"
)

var purposeNames = map[imagev1.ImagePurpose]string{1:"container",2:"development",3:"inference",4:"finetuning",5:"training"}
var acceleratorNames = map[imagev1.AcceleratorKind]string{0:"undeclared",1:"none",2:"nvidia",3:"amd",4:"ascend",5:"other"}
func readScope(v imagev1.ImageScope) (biz.ImageScope,error) {
 switch v {case imagev1.ImageScope_IMAGE_SCOPE_TENANT:return biz.TenantImages,nil;case imagev1.ImageScope_IMAGE_SCOPE_PLATFORM:return biz.PlatformImages,nil}
 return "",biz.Fail(biz.InvalidArgument,"invalid scope")
}
func wireScope(v biz.ImageScope) imagev1.ImageScope {
 switch v {case biz.TenantImages:return imagev1.ImageScope_IMAGE_SCOPE_TENANT;case biz.PlatformImages:return imagev1.ImageScope_IMAGE_SCOPE_PLATFORM};return imagev1.ImageScope_IMAGE_SCOPE_UNSPECIFIED
}
func readDeclarations(p []imagev1.ImagePurpose,a imagev1.AcceleratorKind)([]string,string,error){
 values:=make([]string,0,len(p));for _,v:=range p { name,ok:=purposeNames[v];if !ok{return nil,"",biz.Fail(biz.InvalidArgument,"invalid purpose")};values=append(values,name) }
 name,ok:=acceleratorNames[a];if !ok{return nil,"",biz.Fail(biz.InvalidArgument,"invalid accelerator")};return values,name,nil
}
func readMetadata(name,description string,p []imagev1.ImagePurpose,a imagev1.AcceleratorKind)(biz.Metadata,error){
 purposes,accelerator,err:=readDeclarations(p,a);return biz.Metadata{DisplayName:name,Description:description,Purposes:purposes,Accelerator:accelerator},err
}
func optionalTime(v *time.Time)*timestamppb.Timestamp{if v==nil{return nil};return timestamppb.New(*v)}
func wireSpace(v biz.Space)*imagev1.ImageSpace{return &imagev1.ImageSpace{SpaceId:v.ID,TenantId:v.TenantID,ProjectName:v.ProjectName,RegistryAuthority:v.RegistryAuthority,State:v.State,Reason:string(v.Reason),Version:v.Version,PullCredentialGeneration:v.PullCredentialGeneration,CreatedAt:timestamppb.New(v.CreatedAt),UpdatedAt:timestamppb.New(v.UpdatedAt)}}
func wireCredential(v biz.CredentialInfo)*imagev1.PublisherCredential{return &imagev1.PublisherCredential{TenantId:v.TenantID,SpaceId:v.SpaceID,Generation:v.Generation,Username:v.Username,State:v.State,Version:v.Version,ExpiresAt:optionalTime(v.ExpiresAt),UpdatedAt:timestamppb.New(v.UpdatedAt)}}
func wirePlatform(v biz.ImagePlatform)*imagev1.ImagePlatform{return &imagev1.ImagePlatform{Os:v.OS,Architecture:v.Architecture,Variant:v.Variant}}
func wireRegistration(v biz.Registration)*imagev1.ImageRegistration{
 r:=&imagev1.ImageRegistration{ImageId:v.ID,TenantId:v.TenantID,Scope:wireScope(v.Scope),SpaceId:v.SpaceID,DisplayName:v.Metadata.DisplayName,Description:v.Metadata.Description,Repository:v.Repository,SourceReference:v.SourceReference,Digest:v.Digest,ResolvedReference:v.ResolvedReference,Version:v.Version,CreatedAt:timestamppb.New(v.CreatedAt),UpdatedAt:timestamppb.New(v.UpdatedAt),UnregisteredAt:optionalTime(v.UnregisteredAt)}
 for _,p:=range v.Platforms{r.Platforms=append(r.Platforms,wirePlatform(p))};for _,p:=range v.Metadata.Purposes{for value,name:=range purposeNames{if name==p{r.Purposes=append(r.Purposes,value);break}}};for value,name:=range acceleratorNames{if name==v.Metadata.Accelerator{r.Accelerator=value;break}};return r
}
