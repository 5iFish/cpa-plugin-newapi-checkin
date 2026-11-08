package newapi

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordV2Interoperability(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pub := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	password := strings.Repeat("密码🗝", 40)
	got, e := EncryptPassword(pub, "key-1", password, rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(got, ".")
	if len(parts) != 4 || parts[0] != "v2" {
		t.Fatal("format")
	}
	decode := func(s string) []byte {
		v, e := base64.StdEncoding.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	wrapped := decode(parts[1])
	aesKey, e := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, wrapped, []byte("password-v2"))
	if e != nil || len(aesKey) != 32 {
		t.Fatal(e)
	}
	block, _ := aes.NewCipher(aesKey)
	gcm, _ := cipher.NewGCM(block)
	plain, e := gcm.Open(nil, decode(parts[2]), decode(parts[3]), []byte("password-v2:key-1"))
	if e != nil || string(plain) != password {
		t.Fatal(e)
	}
	if _, e = gcm.Open(nil, decode(parts[2]), decode(parts[3]), []byte("password-v2:wrong")); e == nil {
		t.Fatal("wrong AAD accepted")
	}
	if _, e = rsa.DecryptOAEP(sha256.New(), rand.Reader, key, wrapped, []byte("wrong")); e == nil {
		t.Fatal("wrong OAEP label accepted")
	}
}
func TestLoginChallengeWireFields(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":false}}`))
		} else {
			_, _ = w.Write([]byte(`{"success":true,"data":{"require_verification":true,"flow_token":"sensitive","methods":["totp"]}}`))
		}
	}))
	defer s.Close()
	_, e := NewClient(s.Client()).PasswordLogin(context.Background(), s.URL, "user", "secret")
	kind(t, e, "login_verification_required")
	if strings.Contains(e.Error(), "sensitive") {
		t.Fatal("flow leaked")
	}
}
func TestRefreshOrigin(t *testing.T) {
	var expected string
	expiry := time.Now().Add(5 * 24 * time.Hour).Unix()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sub/api/user/auth/refresh" || r.Header.Get("Origin") != expected {
			t.Error(r.URL.Path, r.Header.Get("Origin"))
		}
		c, e := r.Cookie("new_api_refresh")
		if e != nil || c.Value != "sid.old" {
			t.Error(e)
		}
		http.SetCookie(w, &http.Cookie{Name: "new_api_refresh", Value: "sid.new", Path: "/"})
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"access_token": "access", "access_expires_at": time.Now().Add(15 * time.Minute).Unix(), "session": map[string]any{"sid": "sid", "expires_at": expiry}, "user": map[string]any{"id": 1, "username": "u"}}})
	}))
	defer s.Close()
	expected = s.URL
	v, e := NewClient(s.Client()).Refresh(context.Background(), s.URL+"/sub", "sid.old")
	if e != nil || v.RefreshToken != "sid.new" || v.SessionExpiresAt != expiry {
		t.Fatal(v.SessionExpiresAt, e)
	}
}
func TestRefreshDoesNotExtendSessionExpiry(t *testing.T) { TestRefreshOrigin(t) }
func TestEncryptionDiscoveryDoesNotDowngrade(t *testing.T) {
	post := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			post++
		}
		http.Error(w, "broken", 500)
	}))
	defer s.Close()
	_, e := NewClient(s.Client()).PasswordLogin(context.Background(), s.URL, "u", "p")
	if e == nil || post != 0 {
		t.Fatal(e, post)
	}
}
