package data

import (
 "context"
 "crypto/aes"
 "crypto/cipher"
 "crypto/rand"
 "encoding/json"
 "fmt"
 "regexp"

 biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
)
type AESGCMKeyring struct{active string;keys map[string]cipher.AEAD}
var keyIDPattern=regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
func NewAESGCMKeyring(active string,keys map[string][]byte)(*AESGCMKeyring,error){
 if !keyIDPattern.MatchString(active)||len(keys)==0||len(keys)>8{return nil,fmt.Errorf("invalid Image encryption keyring")}
 ring:=&AESGCMKeyring{active:active,keys:map[string]cipher.AEAD{}}
 for id,key:=range keys{if !keyIDPattern.MatchString(id)||len(key)!=32{return nil,fmt.Errorf("Image requires AES-256 keys")};block,err:=aes.NewCipher(key);if err!=nil{return nil,fmt.Errorf("Image encryption initialization failed")};aead,err:=cipher.NewGCM(block);if err!=nil{return nil,fmt.Errorf("Image encryption initialization failed")};ring.keys[id]=aead}
 if ring.keys[active]==nil{return nil,fmt.Errorf("active Image key missing")};return ring,nil
}
func secretAAD(a biz.SecretAAD)([]byte,error){
 if a.InstallationID=="" || a.SpaceID=="" || a.Generation<1 || (a.Purpose!="publisher" && a.Purpose!="pull") || biz.ParseScope(a.Scope)!=nil || (a.Purpose=="publisher" && a.CommandID=="") {return nil,biz.Fail(biz.InternalError,"invalid Image secret binding")}
 if a.Scope==biz.TenantImages {if _,err:=biz.ParseTenant(a.TenantID);err!=nil{return nil,biz.Fail(biz.InternalError,"invalid Image secret binding")}} else if a.TenantID!="" {return nil,biz.Fail(biz.InternalError,"invalid Image secret binding")}
 return json.Marshal(struct{Version int;Binding biz.SecretAAD}{1,a})
}
func(k *AESGCMKeyring)Seal(ctx context.Context,a biz.SecretAAD,plain biz.Secret)(biz.EncryptedSecret,error){
 if ctx.Err()!=nil{return biz.EncryptedSecret{},biz.Fail(biz.DeadlineExceeded,"encryption canceled")}
 aad,err:=secretAAD(a);if err!=nil{return biz.EncryptedSecret{},err};if len(plain)<1 || len(plain)>4096{return biz.EncryptedSecret{},biz.Fail(biz.InternalError,"invalid Image secret length")}
 g:=k.keys[k.active];nonce:=make([]byte,g.NonceSize());if _,err=rand.Read(nonce);err!=nil{return biz.EncryptedSecret{},biz.Fail(biz.InternalError,"Image entropy unavailable")}
 // Key identifier is authenticated as well as the complete domain binding.
 aad=append(append([]byte(k.active),0),aad...)
 return biz.EncryptedSecret{KeyID:k.active,Ciphertext:g.Seal(nonce,nonce,plain,aad)},nil
}
func(k *AESGCMKeyring)Open(ctx context.Context,a biz.SecretAAD,e biz.EncryptedSecret)(biz.Secret,error){
 if ctx.Err()!=nil{return nil,biz.Fail(biz.DeadlineExceeded,"decryption canceled")}
 aad,err:=secretAAD(a);if err!=nil{return nil,err};g:=k.keys[e.KeyID]
 if g==nil || len(e.Ciphertext)<g.NonceSize()+g.Overhead() || len(e.Ciphertext)>8192{return nil,biz.Fail(biz.InternalError,"Image secret cannot be decrypted")}
 aad=append(append([]byte(e.KeyID),0),aad...)
 plain,err:=g.Open(nil,e.Ciphertext[:g.NonceSize()],e.Ciphertext[g.NonceSize():],aad);if err!=nil{return nil,biz.Fail(biz.InternalError,"Image secret cannot be decrypted")};return biz.Secret(plain),nil
}
var _ biz.SecretCipher=(*AESGCMKeyring)(nil)
