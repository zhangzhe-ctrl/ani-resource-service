package biz

import "context"

type CallerKind string
const (
 GovernanceCaller CallerKind = "governance"
 RuntimeCaller CallerKind = "runtime"
 PlatformCaller CallerKind = "platform"
)
type Caller struct { Kind CallerKind; Subject, Actor, TenantID string }
type callerKey struct{}

// WithCaller is used by the trusted inbound adapter after authenticating its peer.
// Domain code never parses transport metadata or grants a default identity.
func WithCaller(ctx context.Context, caller Caller) context.Context { return context.WithValue(ctx,callerKey{},caller) }
func CallerFromContext(ctx context.Context) (Caller,error) {
 c,ok:=ctx.Value(callerKey{}).(Caller)
 if !ok || !validIdentity(c.Subject) { return Caller{},Fail(TrustedCallerRequired,"trusted caller required") }
 switch c.Kind {
 case GovernanceCaller:
  if !validIdentity(c.Actor) { return Caller{},Fail(TrustedCallerRequired,"actor required") }
  if _,err:=ParseTenant(c.TenantID);err!=nil { return Caller{},Fail(TrustedCallerRequired,"tenant identity required") }
 case RuntimeCaller:
  if c.Actor!="" { return Caller{},Fail(TrustedCallerRequired,"runtime identity required") }
  if _,err:=ParseTenant(c.TenantID);err!=nil { return Caller{},Fail(TrustedCallerRequired,"tenant identity required") }
 case PlatformCaller:
  if c.TenantID!="" || !validIdentity(c.Actor) { return Caller{},Fail(TrustedCallerRequired,"platform operator required") }
 default:return Caller{},Fail(TrustedCallerRequired,"trusted caller required")
 }
 return c,nil
}
func requireKind(ctx context.Context, kind CallerKind, tenant string) (Caller,error) {
 c,err:=CallerFromContext(ctx);if err!=nil{return Caller{},err}
 if c.Kind!=kind {return Caller{},Fail(PermissionDenied,"caller scope denied")}
 if kind!=PlatformCaller {
  if _,err=ParseTenant(tenant);err!=nil{return Caller{},err}
  if c.TenantID!=tenant{return Caller{},Fail(PermissionDenied,"tenant scope denied")}
 }
 return c,nil
}
func RequireTenant(ctx context.Context, tenant string) (Caller,error) {return requireKind(ctx,GovernanceCaller,tenant)}
func RequireRuntime(ctx context.Context, tenant string) (Caller,error) {return requireKind(ctx,RuntimeCaller,tenant)}
func RequirePlatform(ctx context.Context) (Caller,error) {return requireKind(ctx,PlatformCaller,"")}
