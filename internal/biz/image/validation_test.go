package biz

import (
 "context"
 "encoding/json"
 "fmt"
 "reflect"
 "strings"
 "testing"
)

const tenantA="11111111-1111-4111-8111-111111111111"
const tenantB="22222222-2222-4222-8222-222222222222"

func TestValidationMatrix(t *testing.T) {
 digest:="sha256:"+strings.Repeat("a",64)
 for _,v:=range []struct{name,raw string;reason Reason}{
  {"tag","registry.example:5000/t-team/repo:v1",""},
  {"digest","registry.example:5000/t-team/repo@"+digest,""},
  {"external","foreign.example/t-team/repo:v1",InvalidReference},
  {"scheme","https://registry.example:5000/t-team/repo:v1",InvalidReference},
  {"userinfo","u@registry.example:5000/t-team/repo:v1",InvalidReference},
  {"other project","registry.example:5000/t-other/repo:v1",ImageProjectDenied},
  {"platform write","registry.example:5000/platform/repo:v1",ImageProjectDenied},
  {"nested repository","registry.example:5000/t-team/nested/repo:v1",InvalidReference},
  {"encoded slash","registry.example:5000/t-team/repo%2fpart:v1",InvalidReference},
  {"encoded at","registry.example:5000/t-team/repo%40tag:v1",InvalidReference},
  {"traversal","registry.example:5000/t-team/../repo:v1",InvalidReference},
  {"empty tag","registry.example:5000/t-team/repo:",InvalidReference},
  {"no tag","registry.example:5000/t-team/repo",InvalidReference},
  {"query","registry.example:5000/t-team/repo:v1?x=1",InvalidReference},
  {"fragment","registry.example:5000/t-team/repo:v1#x",InvalidReference},
  {"control","registry.example:5000/t-team/repo:v1\n",InvalidReference},
  {"space","registry.example:5000/t-team/repo:v 1",InvalidReference},
  {"extra colon","registry.example:5000/t-team/repo:v1:x",InvalidReference},
  {"extra at","registry.example:5000/t-team/repo@"+digest+"@x",InvalidReference},
  {"uppercase digest","registry.example:5000/t-team/repo@sha256:"+strings.Repeat("A",64),InvalidReference},
  {"long repo","registry.example:5000/t-team/"+strings.Repeat("a",129)+":v1",InvalidReference},
  {"long tag","registry.example:5000/t-team/repo:"+strings.Repeat("a",129),InvalidReference},
  {"long ref",strings.Repeat("a",513),InvalidReference},
 } {t.Run(v.name,func(t *testing.T){got,err:=ParseImageReference("registry.example:5000","t-team",v.raw);if ReasonOf(err)!=v.reason{t.Fatalf("reason=%s want=%s",ReasonOf(err),v.reason)};if err==nil&&got.String()!=v.raw{t.Fatalf("round trip changed: %s",got.String())}})}
 for _,raw:=range []string{"","00000000-0000-0000-0000-000000000000","{11111111-1111-4111-8111-111111111111}","11111111111141118111111111111111","AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA"}{if _,err:=ParseTenant(raw);err==nil{t.Errorf("accepted tenant %q",raw)}}
 if got,err:=ParseTenant(tenantA);err!=nil||got!=tenantA{t.Fatal("valid tenant rejected")}
 for _,raw:=range []string{"ab","Abc","-abc","abc-","a_b","platform","admin",strings.Repeat("a",41)}{if _,err:=ParseSlug(raw);err==nil{t.Errorf("accepted slug %q",raw)}}
 if _,err:=ParseSlug("team-one");err!=nil{t.Fatal(err)}
 for _,raw:=range []string{"img_"+strings.Repeat("0",31),"img_"+strings.Repeat("A",32),"vpc_"+strings.Repeat("a",32)}{if _,err:=ParseImageID(raw);err==nil{t.Errorf("accepted id %q",raw)}}
 if _,err:=ParseImageID("img_"+strings.Repeat("a",32));err!=nil{t.Fatal(err)}
 for _,raw:=range []string{"short","spaces are bad",strings.Repeat("a",129),"test/key"}{if _,err:=ParseIdempotencyKey(raw);err==nil{t.Errorf("accepted key %q",raw)}}
 if _,err:=ParseIdempotencyKey("request-0001");err!=nil{t.Fatal(err)}
 for _,scope:=range []ImageScope{"","all"}{if ParseScope(scope)==nil{t.Fatal("accepted invalid scope")}}
}

func TestMetadataAndFilters(t *testing.T) {
 input:=Metadata{DisplayName:"镜像",Purposes:[]string{"training","container","training"}}
 got,err:=NormalizeMetadata(input);if err!=nil{t.Fatal(err)}
 if got.Accelerator!="undeclared"||!reflect.DeepEqual(got.Purposes,[]string{"container","training"}){t.Fatalf("bad normalized metadata %+v",got)}
 if !reflect.DeepEqual(input.Purposes,[]string{"training","container","training"}){t.Fatal("mutated caller slice")}
 for _,mutate:=range []func(*Metadata){
  func(m *Metadata){m.DisplayName=" "},func(m *Metadata){m.DisplayName=strings.Repeat("字",101)},
  func(m *Metadata){m.Description=strings.Repeat("字",2001)},func(m *Metadata){m.Purposes=nil},
  func(m *Metadata){m.Purposes=[]string{"unknown"}},func(m *Metadata){m.Accelerator="gpu"},
  func(m *Metadata){m.DisplayName="bad\x00name"},func(m *Metadata){m.DisplayName=string([]byte{0xff})},
 } {v:=input;mutate(&v);if _,err:=NormalizeMetadata(v);err==nil{t.Errorf("accepted invalid metadata %+v",v)}}
 f,err:=NormalizeFilter(Filter{Accelerator:"undeclared"});if err!=nil||f.Limit!=20||f.Accelerator!=""{t.Fatal(f,err)}
 for _,f:=range []Filter{{Limit:-1},{Limit:101},{Accelerator:"bogus"},{Purposes:[]string{"bogus"}},{Search:strings.Repeat("字",101)},{Search:"\x00"}}{if _,err:=NormalizeFilter(f);err==nil{t.Errorf("accepted invalid filter %+v",f)}}
}

func TestCallerFailClosed(t *testing.T) {
 ctx:=context.Background()
 for _,check:=range []func(context.Context)(Caller,error){func(c context.Context)(Caller,error){return RequireTenant(c,tenantA)},func(c context.Context)(Caller,error){return RequireRuntime(c,tenantA)},RequirePlatform}{if _,err:=check(ctx);ReasonOf(err)!=TrustedCallerRequired{t.Fatal("anonymous caller accepted",err)}}
 governance:=Caller{Kind:GovernanceCaller,Subject:"ani-governance",Actor:"user:1",TenantID:tenantA}
 if _,err:=RequireTenant(WithCaller(ctx,governance),tenantA);err!=nil{t.Fatal(err)}
 if _,err:=RequireTenant(WithCaller(ctx,governance),tenantB);ReasonOf(err)!=PermissionDenied{t.Fatal(err)}
 if _,err:=RequireRuntime(WithCaller(ctx,governance),tenantA);ReasonOf(err)!=PermissionDenied{t.Fatal(err)}
 if _,err:=RequirePlatform(WithCaller(ctx,governance));ReasonOf(err)!=PermissionDenied{t.Fatal(err)}
 runtime:=Caller{Kind:RuntimeCaller,Subject:"container-owner",TenantID:tenantA}
 if _,err:=RequireRuntime(WithCaller(ctx,runtime),tenantA);err!=nil{t.Fatal(err)}
 if _,err:=RequireTenant(WithCaller(ctx,runtime),tenantA);ReasonOf(err)!=PermissionDenied{t.Fatal(err)}
 platform:=Caller{Kind:PlatformCaller,Subject:"local-operator",Actor:"operator:test"}
 if _,err:=RequirePlatform(WithCaller(ctx,platform));err!=nil{t.Fatal(err)}
 for _,bad:=range []Caller{
  {},{Kind:GovernanceCaller,Subject:"ani-governance",Actor:"user:1"},
  {Kind:GovernanceCaller,Subject:"ani-governance",Actor:"user:1",TenantID:"00000000-0000-0000-0000-000000000000"},
  {Kind:PlatformCaller,Subject:"local-operator",Actor:"operator:test",TenantID:tenantA},
  {Kind:RuntimeCaller,Subject:"container-owner",Actor:"user:1",TenantID:tenantA},
  {Kind:"unknown",Subject:"unknown"},
 }{if _,err:=CallerFromContext(WithCaller(ctx,bad));err==nil{t.Fatal("invalid identity accepted",bad)}}
}

func TestSecretRedactionAndErrorReason(t *testing.T) {
 secret:=Secret("sentinel-plain-secret")
 for _,format:=range []string{"%s","%v","%+v","%#v","%q","%x"}{if strings.Contains(fmt.Sprintf(format,secret),string(secret)){t.Fatal("secret formatting leaked")}}
 for _,value:=range []any{secret,PullMaterial{Secret:secret},CredentialDelivery{Secret:secret}}{body,err:=json.Marshal(value);if err!=nil||strings.Contains(string(body),string(secret)){t.Fatal("secret encoding leaked")}}
 if ReasonOf(fmt.Errorf("wrapped: %w",Fail(VersionConflict,"version changed")))!=VersionConflict{t.Fatal("lost reason")}
 if ReasonOf(fmt.Errorf("unknown"))!=InternalError{t.Fatal("unknown error not safe")}
}
