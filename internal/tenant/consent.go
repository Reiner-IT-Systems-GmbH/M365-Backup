package tenant

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ConsentState struct {
	TenantID  string `json:"t"`
	ExpiresAt int64  `json:"e"`
}

func SignConsentState(tenantID, masterKeyB64 string, ttl time.Duration) (string, error) {
	payload, err := json.Marshal(ConsentState{
		TenantID:  tenantID,
		ExpiresAt: time.Now().Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(masterKeyB64))
	_, _ = mac.Write(payload)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func VerifyConsentState(state, masterKeyB64 string) (string, error) {
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid state")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(masterKeyB64))
	_, _ = mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", fmt.Errorf("bad signature")
	}
	var cs ConsentState
	if err := json.Unmarshal(payload, &cs); err != nil {
		return "", err
	}
	if time.Now().Unix() > cs.ExpiresAt {
		return "", fmt.Errorf("state expired")
	}
	return cs.TenantID, nil
}
