package data

import (
 "context"
 "encoding/json"
 "errors"

 "github.com/jackc/pgx/v5"
 biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
 "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image/sqlcgen"
)

func(p *Postgres) applyPlatformCatalog(ctx context.Context,c biz.Command,change func(*sqlcgen.Queries,biz.Space)(biz.Registration,error))(biz.Registration,error){
 if err:=platformScope(c.Scope,c.TenantID);err!=nil{return biz.Registration{},err}
 var result biz.Registration
 err:=p.platformTransaction(ctx,func(q *sqlcgen.Queries)error{
  row,e:=q.GetPlatformSpace(ctx);if e!=nil{return databaseError(e)}
  if row.SpaceID!=c.SpaceID{return biz.Fail(biz.ImageNotFound,"platform space not found")}
  stored,e:=reservePlatformCommand(ctx,q,c);if e!=nil{return e}
  if stored.State=="succeeded"{
   r:=stored.Result.Registration
   if r==nil || r.Scope!=biz.PlatformImages || r.TenantID!="" || r.SpaceID!=c.SpaceID{return biz.Fail(biz.InternalError,"invalid platform catalog result")}
   result=*r;return nil
  }
  if stored.State!="pending"{return biz.Fail(biz.RequestInProgress,"catalog request is not pending")}
  result,e=change(q,fromSpace(row));if e!=nil{return e}
  stored.Result=biz.CommandResult{Registration:&result}
  _,e=completePlatformCommand(ctx,q,stored);return e
 })
 return result,err
}
func(p *Postgres) ApplyPlatformRegistration(ctx context.Context,c biz.Command,r biz.Registration)(biz.Registration,error){
 if c.Kind!="register_image" || r.Scope!=biz.PlatformImages || r.TenantID!="" || r.SpaceID!=c.SpaceID{return biz.Registration{},biz.Fail(biz.InvalidArgument,"invalid platform registration ownership")}
 return p.applyPlatformCatalog(ctx,c,func(q *sqlcgen.Queries,s biz.Space)(biz.Registration,error){
  if s.State!="available"{return biz.Registration{},biz.Fail(biz.SpaceNotReady,"platform space not ready")}
  platforms,err:=json.Marshal(r.Platforms);if err!=nil{return biz.Registration{},biz.Fail(biz.InternalError,"platform encoding failed")}
  row,err:=q.InsertPlatformRegistration(ctx,sqlcgen.InsertPlatformRegistrationParams{ImageID:r.ID,SpaceID:r.SpaceID,DisplayName:r.Metadata.DisplayName,Description:r.Metadata.Description,Repository:r.Repository,SourceReference:r.SourceReference,Digest:r.Digest,MediaType:r.MediaType,Platforms:platforms,Purposes:r.Metadata.Purposes,Accelerator:r.Metadata.Accelerator,Actor:c.Actor})
  if err!=nil{return biz.Registration{},databaseError(err)}
  return fromRegistration(row,s)
 })
}
func(p *Postgres) ApplyPlatformMetadata(ctx context.Context,c biz.Command,r biz.UpdateImage)(biz.Registration,error){
 if c.Kind!="update_image" || r.TenantID!=""{return biz.Registration{},biz.Fail(biz.InvalidArgument,"invalid platform metadata ownership")}
 return p.applyPlatformCatalog(ctx,c,func(q *sqlcgen.Queries,s biz.Space)(biz.Registration,error){
  row,err:=q.UpdatePlatformMetadata(ctx,sqlcgen.UpdatePlatformMetadataParams{ImageID:r.ImageID,DisplayName:r.Metadata.DisplayName,Description:r.Metadata.Description,Purposes:r.Metadata.Purposes,Accelerator:r.Metadata.Accelerator,Actor:c.Actor,ExpectedVersion:r.ExpectedVersion})
  if errors.Is(err,pgx.ErrNoRows){return biz.Registration{},platformCASFailure(ctx,q,r.ImageID)}
  if err!=nil{return biz.Registration{},databaseError(err)}
  return fromRegistration(row,s)
 })
}
func(p *Postgres) ApplyPlatformUnregister(ctx context.Context,c biz.Command,r biz.UnregisterImage)(biz.Registration,error){
 if c.Kind!="unregister_image" || r.TenantID!=""{return biz.Registration{},biz.Fail(biz.InvalidArgument,"invalid platform unregister ownership")}
 return p.applyPlatformCatalog(ctx,c,func(q *sqlcgen.Queries,s biz.Space)(biz.Registration,error){
  existing,err:=q.GetPlatformRegistration(ctx,sqlcgen.GetPlatformRegistrationParams{ImageID:r.ImageID});if err!=nil{return biz.Registration{},databaseError(err)}
  if existing.UnregisteredAt!=nil{return fromRegistration(existing,s)}
  row,err:=q.UnregisterPlatformRegistration(ctx,sqlcgen.UnregisterPlatformRegistrationParams{ImageID:r.ImageID,Actor:c.Actor,ExpectedVersion:r.ExpectedVersion})
  if errors.Is(err,pgx.ErrNoRows){return biz.Registration{},platformCASFailure(ctx,q,r.ImageID)}
  if err!=nil{return biz.Registration{},databaseError(err)}
  return fromRegistration(row,s)
 })
}
func platformCASFailure(ctx context.Context,q *sqlcgen.Queries,id string)error{
 row,err:=q.GetPlatformRegistration(ctx,sqlcgen.GetPlatformRegistrationParams{ImageID:id});if err!=nil{return databaseError(err)}
 if row.UnregisteredAt!=nil{return biz.Fail(biz.ImageNotFound,"image is unregistered")}
 return biz.Fail(biz.VersionConflict,"image version changed")
}
