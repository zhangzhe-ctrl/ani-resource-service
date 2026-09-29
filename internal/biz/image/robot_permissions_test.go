package biz
import "testing"
func TestRestrictedRobotPermissions(t *testing.T){
 for _,purpose:=range []string{"publisher","pull"}{p,err:=RobotPermissions("t-alpha","platform",purpose,TenantImages);if err!=nil||ValidateRobotPermissions(p,p)!=nil{t.Fatal(err)};for _,bad:=range [][]RobotPermission{{{"*","repository","pull"}},{{"t-alpha","repository","push"}},{{"t-alpha","repository","delete"}},append(append([]RobotPermission{},p...),RobotPermission{"t-other","repository","pull"})}{if ValidateRobotPermissions(bad,p)==nil{t.Fatal("broadened permissions accepted")}}}
 p,err:=RobotPermissions("platform","platform","publisher",PlatformImages);if err!=nil||len(p)!=2{t.Fatal(err)}
}
