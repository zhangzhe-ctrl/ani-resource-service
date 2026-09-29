package biz

import "sort"

// RobotPermissions builds only the three approved identities. A system-level
// robot can span explicit projects, but has no system-scoped permissions.
func RobotPermissions(project,platform,purpose string,scope ImageScope)([]RobotPermission,error){
 if project=="" || platform=="" || project=="*" || platform=="*" || ParseScope(scope)!=nil || (scope==PlatformImages && (project!=platform || purpose!="publisher")) || (scope==TenantImages && project==platform) {return nil,Fail(InvalidArgument,"invalid credential project binding")}
 out:=[]RobotPermission{{project,"repository","pull"}}
 switch purpose{case "publisher":out=append(out,RobotPermission{project,"repository","push"});case "pull":default:return nil,Fail(InvalidArgument,"invalid credential purpose")}
 if scope==TenantImages {out=append(out,RobotPermission{platform,"repository","pull"})};return out,nil
}
func ValidateRobotPermissions(actual,expected []RobotPermission)error{
 keys:=func(values []RobotPermission)([]string,bool){
  if len(values)==0||len(values)>3{return nil,false};out:=make([]string,0,len(values));seen:=map[string]bool{};pull:=map[string]bool{}
  for _,v:=range values{if v.Project==""||v.Project=="*"||v.Resource!="repository"||(v.Action!="pull"&&v.Action!="push"){return nil,false};key:=v.Project+"/"+v.Resource+"/"+v.Action;if seen[key]{return nil,false};seen[key]=true;out=append(out,key);if v.Action=="pull"{pull[v.Project]=true}}
  for _,v:=range values{if v.Action=="push"&&!pull[v.Project]{return nil,false}}
  sort.Strings(out);return out,true
 }
 a,ok:=keys(actual);b,want:=keys(expected);if !ok||!want||len(a)!=len(b){return Fail(PermissionDenied,"robot permission mismatch")};for i:=range a{if a[i]!=b[i]{return Fail(PermissionDenied,"robot permission mismatch")}};return nil
}
