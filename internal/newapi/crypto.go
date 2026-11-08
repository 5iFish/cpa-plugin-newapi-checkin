package newapi

import (
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
)

func EncryptPassword(publicKeyPEM, kid, password string, random io.Reader) (string, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil || kid == "" {
		return "", core.Error("unsupported_site")
	}
	key, e := x509.ParsePKIXPublicKey(block.Bytes)
	if e != nil {
		return "", core.Error("unsupported_site")
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok || pub.N.BitLen() < 2048 || pub.N.BitLen() > 8192 {
		return "", core.Error("unsupported_site")
	}
	aesKey := make([]byte, 32)
	nonce := make([]byte, 12)
	if _, e = io.ReadFull(random, aesKey); e != nil {
		return "", e
	}
	if _, e = io.ReadFull(random, nonce); e != nil {
		return "", e
	}
	wrapped, e := rsa.EncryptOAEP(sha256.New(), random, pub, aesKey, []byte("password-v2"))
	if e != nil {
		return "", core.Error("unsupported_site")
	}
	b, _ := aes.NewCipher(aesKey)
	gcm, _ := cipher.NewGCM(b)
	encrypted := gcm.Seal(nil, nonce, []byte(password), []byte("password-v2:"+kid))
	encode := base64.StdEncoding.EncodeToString
	return "v2." + encode(wrapped) + "." + encode(nonce) + "." + encode(encrypted), nil
}
