package biz

import (
 "regexp"
 "sort"
 "strings"
 "unicode"
 "unicode/utf8"
)

var (
 tenantPattern=regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
 slugPattern=regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)
 imageIDPattern=regexp.MustCompile(`^img_[0-9a-f]{32}$`)
 repositoryPattern=regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)
 tagPattern=regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
 digestPattern=regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
 keyPattern=regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)
)
func ParseTenant(raw string)(string,error) {
 if !tenantPattern.MatchString(raw)||raw=="00000000-0000-0000-0000-000000000000" {return "",Fail(InvalidArgument,"canonical nonzero tenant UUID required")}
 return raw,nil
}
func ParseSlug(raw string)(string,error) {
 if !slugPattern.MatchString(raw) {return "",Fail(InvalidArgument,"invalid space slug")}
 switch raw {case "platform","admin","administrator","library","system","harbor","registry","public":return "",Fail(InvalidArgument,"reserved space slug")}
 return raw,nil
}
func ParseImageID(raw string)(string,error) {
 if !imageIDPattern.MatchString(raw) {return "",Fail(InvalidArgument,"invalid image ID")};return raw,nil
}
func ParseIdempotencyKey(raw string)(string,error) {
 if !keyPattern.MatchString(raw) {return "",Fail(InvalidArgument,"invalid idempotency key")};return raw,nil
}
func ParseScope(scope ImageScope) error {
 if scope!=TenantImages&&scope!=PlatformImages{return Fail(InvalidArgument,"explicit image scope required")};return nil
}
func ParseDigest(raw string) error {
 if !digestPattern.MatchString(raw){return Fail(InvalidReference,"invalid image digest")};return nil
}
func ParseImageReference(authority,project,raw string)(ImageReference,error) {
 invalid:=func()(ImageReference,error){return ImageReference{},Fail(InvalidReference,"invalid image reference")}
 if len(raw)>512||authority==""||project==""||strings.ContainsAny(raw,"%?#\\")||strings.Contains(raw,"://") {return invalid()}
 for _,c:=range raw {if unicode.IsSpace(c)||unicode.IsControl(c){return invalid()}}
 parts:=strings.Split(raw,"/")
 if len(parts)!=3||parts[0]!=authority {return invalid()}
 if parts[1]!=project {return ImageReference{},Fail(ImageProjectDenied,"image project denied")}
 repo,ref,found:=strings.Cut(parts[2],"@")
 digest:=found
 if found {if ParseDigest(ref)!=nil{return invalid()}} else {
  repo,ref,found=strings.Cut(parts[2],":")
  if !found||!tagPattern.MatchString(ref){return invalid()}
 }
 if len(repo)>128||!repositoryPattern.MatchString(repo){return invalid()}
 return ImageReference{Authority:authority,Project:project,Repository:repo,Reference:ref,IsDigest:digest},nil
}
func validIdentity(raw string)bool {
 if raw==""||len(raw)>128||!utf8.ValidString(raw){return false}
 for _,c:=range raw{if unicode.IsControl(c)||unicode.IsSpace(c){return false}}
 return true
}
func NormalizePurposes(values []string, required bool)([]string,error) {
 if len(values)>5||(required&&len(values)==0){return nil,Fail(InvalidArgument,"invalid purposes")}
 seen:=map[string]bool{};out:=make([]string,0,len(values))
 for _,v:=range values{
  switch v{case "container","development","inference","finetuning","training":default:return nil,Fail(InvalidArgument,"invalid purpose")}
  if !seen[v]{out=append(out,v);seen[v]=true}
 }
 sort.Strings(out);return out,nil
}
func validAccelerator(raw string)bool {switch raw{case "undeclared","none","nvidia","amd","ascend","other":return true};return false}
func NormalizeMetadata(m Metadata)(Metadata,error) {
 if !utf8.ValidString(m.DisplayName)||!utf8.ValidString(m.Description)||strings.TrimSpace(m.DisplayName)==""||utf8.RuneCountInString(m.DisplayName)>100||utf8.RuneCountInString(m.Description)>2000{return Metadata{},Fail(InvalidArgument,"invalid display name or description")}
 for _,c:=range m.DisplayName{if unicode.IsControl(c){return Metadata{},Fail(InvalidArgument,"invalid display name")}}
 for _,c:=range m.Description{if unicode.IsControl(c)&&c!='\n'&&c!='\t'{return Metadata{},Fail(InvalidArgument,"invalid description")}}
 var err error;m.Purposes,err=NormalizePurposes(m.Purposes,true);if err!=nil{return Metadata{},err}
 if m.Accelerator=="" {m.Accelerator="undeclared"}
 if !validAccelerator(m.Accelerator){return Metadata{},Fail(InvalidArgument,"invalid accelerator")}
 return m,nil
}
func NormalizeFilter(f Filter)(Filter,error) {
 if !utf8.ValidString(f.Search)||utf8.RuneCountInString(f.Search)>100 {return Filter{},Fail(InvalidArgument,"invalid search")}
 for _,c:=range f.Search{if unicode.IsControl(c){return Filter{},Fail(InvalidArgument,"invalid search")}}
 if f.Limit==0{f.Limit=20};if f.Limit<1||f.Limit>100{return Filter{},Fail(InvalidArgument,"invalid page limit")}
 var err error;f.Purposes,err=NormalizePurposes(f.Purposes,false);if err!=nil{return Filter{},err}
 if f.Accelerator=="undeclared"{f.Accelerator=""};if f.Accelerator!=""&&!validAccelerator(f.Accelerator){return Filter{},Fail(InvalidArgument,"invalid accelerator")}
 return f,nil
}
