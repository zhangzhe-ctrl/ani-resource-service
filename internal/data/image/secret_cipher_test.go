package data

import (
	"context"
	"encoding/json"
	"fmt"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"strings"
	"testing"
)

func TestCipherWrongAADTamperKeyMissing(t *testing.T) {
	key := []byte(strings.Repeat("x", 32))
	ring, err := NewAESGCMKeyring("v2", map[string][]byte{"v1": []byte(strings.Repeat("y", 32)), "v2": key})
	if err != nil {
		t.Fatal(err)
	}
	a := biz.SecretAAD{InstallationID: "installation", Scope: biz.TenantImages, TenantID: "11111111-1111-4111-8111-111111111111", SpaceID: "space", Purpose: "publisher", Generation: 1, CommandID: "command"}
	cipher, err := ring.Seal(context.Background(), a, biz.Secret(sentinelSecret))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := ring.Seal(context.Background(), a, biz.Secret(sentinelSecret))
	if string(cipher.Ciphertext) == string(again.Ciphertext) {
		t.Fatal("nonce reused")
	}
	plain, err := ring.Open(context.Background(), a, cipher)
	if err != nil || string(plain) != sentinelSecret {
		t.Fatal("roundtrip", err)
	}
	variants := []biz.SecretAAD{a, a, a, a, a, a, a}
	variants[0].InstallationID = "other"
	variants[1].TenantID = "22222222-2222-4222-8222-222222222222"
	variants[2].SpaceID = "other"
	variants[3].Purpose = "pull"
	variants[4].Generation = 2
	variants[5].CommandID = "other"
	variants[6].Scope = biz.PlatformImages
	variants[6].TenantID = ""
	for _, wrong := range variants {
		if _, err = ring.Open(context.Background(), wrong, cipher); err == nil {
			t.Fatal("wrong AAD accepted")
		}
	}
	for _, change := range []func(*biz.EncryptedSecret){func(e *biz.EncryptedSecret) { e.KeyID = "missing" }, func(e *biz.EncryptedSecret) { e.KeyID = "v1" }, func(e *biz.EncryptedSecret) { e.Ciphertext[0] ^= 1 }, func(e *biz.EncryptedSecret) { e.Ciphertext = e.Ciphertext[:3] }} {
		bad := cipher
		bad.Ciphertext = append([]byte(nil), cipher.Ciphertext...)
		change(&bad)
		if _, err = ring.Open(context.Background(), a, bad); err == nil || strings.Contains(fmt.Sprint(err), sentinelSecret) {
			t.Fatal("bad ciphertext accepted or leaked")
		}
	}
	for _, v := range []any{plain, biz.CredentialDelivery{Secret: plain}, biz.Command{DeliverySecret: cipher}, biz.CommandResult{}, biz.Candidate{}} {
		j, _ := json.Marshal(v)
		if strings.Contains(string(j), sentinelSecret) || strings.Contains(fmt.Sprintf("%+v", v), sentinelSecret) {
			t.Fatal("secret in generic encoding")
		}
	}
	if _, err = NewAESGCMKeyring("v1", map[string][]byte{"v1": []byte("short")}); err == nil {
		t.Fatal("weak key accepted")
	}
}
