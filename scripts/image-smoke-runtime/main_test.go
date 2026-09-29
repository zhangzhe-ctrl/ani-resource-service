package main

import (
 "os"
 "path/filepath"
 "testing"
)
func TestProfileRequiresApprovalAndScopedTenant(t *testing.T){
 p:=profile{Format:"image-mvp-live-profile/v1",Approved:true,Approval:"review-1",RunID:"smoke-1"};p.Smoke.RuntimeConfig="/private/image.json";p.Smoke.Technical=true
 p.Tenants=append(p.Tenants,struct{ID string `json:"tenant_id"`}{"tenant-a"})
 if validateProfile(p,"tenant-a")!=nil{t.Fatal("approved tenant rejected")}
 if validateProfile(p,"tenant-b")==nil{t.Fatal("foreign tenant accepted")}
 p.Approved=false;if validateProfile(p,"tenant-a")==nil{t.Fatal("unapproved profile accepted")}
 p.Approved=true;p.Smoke.Technical=false;if validateProfile(p,"tenant-a")==nil{t.Fatal("unapproved runtime read accepted")}
}
func TestPrivateInputAndExclusiveOutput(t *testing.T){
 dir:=t.TempDir();if err:=os.Chmod(dir,0700);err!=nil{t.Fatal(err)}
 name:=filepath.Join(dir,"auth.json");f,err:=reserveOutput(name);if err!=nil{t.Fatal(err)};f.Close()
 if _,err=reserveOutput(name);err==nil{t.Fatal("existing output replaced")}
 if err=os.WriteFile(name,[]byte("secret"),0600);err!=nil{t.Fatal(err)}
 if _,err=privateFile(name);err!=nil{t.Fatal(err)}
 link:=filepath.Join(dir,"link");if err=os.Symlink(name,link);err!=nil{t.Fatal(err)}
 if _,err=privateFile(link);err==nil{t.Fatal("symlink input accepted")}
 if _,err=reserveOutput(link);err==nil{t.Fatal("symlink output replaced")}
 if err=os.Chmod(name,0644);err!=nil{t.Fatal(err)}
 if _,err=privateFile(name);err==nil{t.Fatal("public secret input accepted")}
 if err=os.Chmod(dir,0755);err!=nil{t.Fatal(err)}
 if _,err=reserveOutput(filepath.Join(dir,"new"));err==nil{t.Fatal("public output directory accepted")}
}
func TestRequestRejectsInvalidScopePlatformAndInjectedFields(t *testing.T){
 valid:=`{"tenant_id":"10000000-0000-4000-8000-000000000001","image_id":"img_20000000000040008000000000000001","scope":"tenant","platform":{"OS":"linux","Architecture":"amd64","Variant":""}}`
 if _,err:=decodeRequest([]byte(valid));err!=nil{t.Fatal(err)}
 for _,raw:=range []string{`{}`,valid+`{}`,`{"scope":"platform","secret":"injected"}`,`{"tenant_id":"bad"}`} {if _,err:=decodeRequest([]byte(raw));err==nil{t.Fatal("invalid request accepted")}}
}
