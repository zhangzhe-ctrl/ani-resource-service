package biz

import (
 "context"
 "strings"
 "time"

 "github.com/google/uuid"
)

type Catalog struct {repo CatalogRepository;spaces SpaceRepository;commands CommandRepository;registry Registry;cursor *CursorCodec}
func NewCatalog(repo CatalogRepository,spaces SpaceRepository,commands CommandRepository,registry Registry,cursor *CursorCodec)(*Catalog,error){if repo==nil||spaces==nil||commands==nil||registry==nil||cursor==nil{return nil,Fail(InvalidArgument,"catalog dependencies required")};return &Catalog{repo:repo,spaces:spaces,commands:commands,registry:registry,cursor:cursor},nil}
func(c *Catalog)RegisterImage(ctx context.Context,in RegisterImage)(Registration,error){
 caller,err:=RequireTenant(ctx,in.TenantID);if err!=nil{return Registration{},err};in.Metadata,err=NormalizeMetadata(in.Metadata);if err!=nil{return Registration{},err};if _,err=ParseIdempotencyKey(in.IdempotencyKey);err!=nil{return Registration{},err}
 s,err:=c.spaces.FindTenantSpace(ctx,in.TenantID);if err!=nil{return Registration{},err};if s.Scope!=TenantImages||s.TenantID!=in.TenantID||s.State!="available"||s.ProjectID<=0{return Registration{},Fail(SpaceNotReady,"image space not ready")}
 ref,err:=ParseImageReference(s.RegistryAuthority,s.ProjectName,in.ImageReference);if err!=nil{return Registration{},err};in.ImageReference=ref.String();command,err:=requestCommand(caller,s.ID,"register_image",in.IdempotencyKey,in);if err!=nil{return Registration{},err}
 // Read a completed command before resolving a mutable tag or contacting the
 // registry. The data transaction repeats this guard for concurrent requests.
 existing,err:=c.commands.FindTenantCommand(ctx,in.TenantID,s.ID,in.IdempotencyKey)
 if err==nil{
  if existing.Kind!=command.Kind||existing.Actor!=command.Actor||existing.Fingerprint!=command.Fingerprint{return Registration{},Fail(IdempotencyConflict,"idempotency key belongs to another request")}
  if existing.State!="succeeded"{return Registration{},Fail(RequestInProgress,"catalog request not completed")};if existing.Result.Registration==nil{return Registration{},Fail(InternalError,"missing registration result")};r:=*existing.Result.Registration;if err=validateRegistrationOwner(r,in.TenantID,TenantImages);err!=nil{return Registration{},err};return r,nil
 };if ReasonOf(err)!=ImageNotFound{return Registration{},err}
 ctx,cancel:=context.WithTimeout(ctx,45*time.Second);defer cancel()
 artifact,err:=c.registry.ResolveArtifact(ctx,s.ProjectName,ref.Repository,ref.Reference);if err!=nil{return Registration{},err};if err=validateArtifact(artifact);err!=nil{return Registration{},err};if ref.IsDigest&&ref.Reference!=artifact.Digest{return Registration{},Fail(DependencyUnavailable,"registry digest mismatch")}
 r:=Registration{ID:"img_"+strings.ReplaceAll(uuid.NewString(),"-",""),TenantID:in.TenantID,Scope:TenantImages,SpaceID:s.ID,Repository:ref.Repository,SourceReference:ref.String(),Digest:artifact.Digest,MediaType:artifact.MediaType,Platforms:artifact.Platforms,Metadata:in.Metadata}
 return c.repo.ApplyTenantRegistration(ctx,command,r)
}
func validateRegistrationOwner(r Registration,tenant string,scope ImageScope)error{
 if r.Scope!=scope||(scope==TenantImages&&r.TenantID!=tenant)||(scope==PlatformImages&&r.TenantID!=""){return Fail(InternalError,"stored image ownership mismatch")};return nil
}
func readRegistration(ctx context.Context,repo CatalogRepository,in ReadImage)(Registration,error){
 if _,err:=ParseTenant(in.TenantID);err!=nil{return Registration{},err};if err:=ParseScope(in.Scope);err!=nil{return Registration{},err};if _,err:=ParseImageID(in.ImageID);err!=nil{return Registration{},err}
 var r Registration;var err error;if in.Scope==TenantImages{r,err=repo.FindTenantRegistration(ctx,in.TenantID,in.ImageID)}else{r,err=repo.FindPlatformRegistration(ctx,in.ImageID)};if err!=nil{return Registration{},err};if r.ID!=in.ImageID{return Registration{},Fail(InternalError,"stored image ID mismatch")};if err=validateRegistrationOwner(r,in.TenantID,in.Scope);err!=nil{return Registration{},err};return r,nil
}
func(c *Catalog)GetImage(ctx context.Context,in ReadImage)(Registration,error){if _,err:=RequireTenant(ctx,in.TenantID);err!=nil{return Registration{},err};return readRegistration(ctx,c.repo,in)}
func(c *Catalog)ListImages(ctx context.Context,in ListImages)(RegistrationPage,error){
 if _,err:=RequireTenant(ctx,in.TenantID);err!=nil{return RegistrationPage{},err};if err:=ParseScope(in.Scope);err!=nil{return RegistrationPage{},err};var err error;in.Filter,err=NormalizeFilter(in.Filter);if err!=nil{return RegistrationPage{},err};after,err:=c.cursor.DecodeCursor(in.Cursor,in.TenantID,in.Scope,in.Filter);if err!=nil{return RegistrationPage{},err}
 var rows []Registration;if in.Scope==TenantImages{rows,err=c.repo.PageTenantRegistrations(ctx,in.TenantID,in.Filter,after)}else{rows,err=c.repo.PagePlatformRegistrations(ctx,in.Filter,after)};if err!=nil{return RegistrationPage{},err}
 for _,r:=range rows{if err=validateRegistrationOwner(r,in.TenantID,in.Scope);err!=nil{return RegistrationPage{},err}}
 page:=RegistrationPage{Items:rows};if len(rows)>in.Filter.Limit{page.Items=rows[:in.Filter.Limit];last:=page.Items[len(page.Items)-1];page.NextCursor,err=c.cursor.EncodeCursor(in.TenantID,in.Scope,in.Filter,PageKey{CreatedAt:last.CreatedAt,ImageID:last.ID});if err!=nil{return RegistrationPage{},err}};if page.Items==nil{page.Items=[]Registration{}};return page,nil
}
func(c *Catalog)UpdateImage(ctx context.Context,in UpdateImage)(Registration,error){
 caller,err:=RequireTenant(ctx,in.TenantID);if err!=nil{return Registration{},err};if _,err=ParseImageID(in.ImageID);err!=nil{return Registration{},err};if in.ExpectedVersion<1{return Registration{},Fail(InvalidArgument,"positive expected version required")};in.Metadata,err=NormalizeMetadata(in.Metadata);if err!=nil{return Registration{},err};if _,err=ParseIdempotencyKey(in.IdempotencyKey);err!=nil{return Registration{},err}
 s,err:=c.spaces.FindTenantSpace(ctx,in.TenantID);if err!=nil{return Registration{},err};command,err:=requestCommand(caller,s.ID,"update_image",in.IdempotencyKey,in);if err!=nil{return Registration{},err};return c.repo.ApplyTenantMetadata(ctx,command,in)
}
func(c *Catalog)UnregisterImage(ctx context.Context,in UnregisterImage)(Registration,error){
 caller,err:=RequireTenant(ctx,in.TenantID);if err!=nil{return Registration{},err};if _,err=ParseImageID(in.ImageID);err!=nil{return Registration{},err};if in.ExpectedVersion<1{return Registration{},Fail(InvalidArgument,"positive expected version required")};if _,err=ParseIdempotencyKey(in.IdempotencyKey);err!=nil{return Registration{},err};s,err:=c.spaces.FindTenantSpace(ctx,in.TenantID);if err!=nil{return Registration{},err};command,err:=requestCommand(caller,s.ID,"unregister_image",in.IdempotencyKey,in);if err!=nil{return Registration{},err};return c.repo.ApplyTenantUnregister(ctx,command,in)
}
