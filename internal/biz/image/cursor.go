package biz

import (
 "bytes"
 "crypto/hmac"
 "crypto/sha256"
 "encoding/base64"
 "encoding/hex"
 "encoding/json"
 "io"
 "strings"
)

type CursorCodec struct{key []byte}
type cursorPayload struct{Version int `json:"v"`;Tenant string `json:"t"`;Scope ImageScope `json:"s"`;Binding string `json:"f"`;After PageKey `json:"a"`}
func NewCursorCodec(key []byte)(*CursorCodec,error){if len(key)<32||len(key)>128{return nil,Fail(InvalidArgument,"cursor signing key must contain at least 32 bytes")};return &CursorCodec{key:append([]byte(nil),key...)},nil}
func cursorBinding(tenant string,scope ImageScope,f Filter)(string,error){
 if _,err:=ParseTenant(tenant);err!=nil{return "",err};if err:=ParseScope(scope);err!=nil{return "",err};normalized,err:=NormalizeFilter(f);if err!=nil{return "",err};body,err:=json.Marshal(normalized);if err!=nil{return "",Fail(InternalError,"cursor filter encoding failed")};sum:=sha256.Sum256(body);return hex.EncodeToString(sum[:]),nil
}
func(c *CursorCodec)EncodeCursor(tenant string,scope ImageScope,f Filter,after PageKey)(string,error){
 binding,err:=cursorBinding(tenant,scope,f);if err!=nil{return "",err};if after.CreatedAt.IsZero(){return "",Fail(InvalidCursor,"invalid cursor position")};if _,err=ParseImageID(after.ImageID);err!=nil{return "",Fail(InvalidCursor,"invalid cursor position")}
 body,err:=json.Marshal(cursorPayload{Version:1,Tenant:tenant,Scope:scope,Binding:binding,After:after});if err!=nil{return "",Fail(InternalError,"cursor encoding failed")};mac:=hmac.New(sha256.New,c.key);_,_=mac.Write(body);return base64.RawURLEncoding.EncodeToString(body)+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),nil
}
func(c *CursorCodec)DecodeCursor(raw,tenant string,scope ImageScope,f Filter)(*PageKey,error){
 binding,err:=cursorBinding(tenant,scope,f);if err!=nil{return nil,err};if raw==""{return nil,nil};invalid:=func()(*PageKey,error){return nil,Fail(InvalidCursor,"invalid or mismatched image cursor")}
 if len(raw)>4096{return invalid()};parts:=strings.Split(raw,".");if len(parts)!=2{return invalid()};body,err:=base64.RawURLEncoding.Strict().DecodeString(parts[0]);if err!=nil{return invalid()};signature,err:=base64.RawURLEncoding.Strict().DecodeString(parts[1]);if err!=nil||len(signature)!=sha256.Size{return invalid()};mac:=hmac.New(sha256.New,c.key);_,_=mac.Write(body);if !hmac.Equal(signature,mac.Sum(nil)){return invalid()}
 var payload cursorPayload;decoder:=json.NewDecoder(bytes.NewReader(body));decoder.DisallowUnknownFields();if decoder.Decode(&payload)!=nil||decoder.Decode(new(any))!=io.EOF{return invalid()}
 if payload.Version!=1||payload.Tenant!=tenant||payload.Scope!=scope||payload.Binding!=binding||payload.After.CreatedAt.IsZero(){return invalid()};if _,err=ParseImageID(payload.After.ImageID);err!=nil{return invalid()};return &payload.After,nil
}
