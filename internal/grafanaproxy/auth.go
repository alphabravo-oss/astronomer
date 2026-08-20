package grafanaproxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const grafanaAuthCookie = "grafana_auth"

var errGrafanaAuthInvalid = errors.New("grafana_auth is invalid")

type grafanaAuth struct {
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	Explore    bool     `json:"explore"`
	Admin      bool     `json:"admin"`
	ClusterIDs []string `json:"clusterIds,omitempty"`
	Exp        int64    `json:"exp"`
}

func signGrafanaAuth(key []byte, auth grafanaAuth) (string, error) {
	if len(key) == 0 || auth.Email == "" || auth.Role == "" || auth.Exp == 0 {
		return "", errGrafanaAuthInvalid
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyGrafanaAuth(key []byte, cookie string) (grafanaAuth, error) {
	var zero grafanaAuth
	if len(key) == 0 {
		return zero, errGrafanaAuthInvalid
	}
	parts := strings.Split(cookie, ".")
	if len(parts) != 2 {
		return zero, errGrafanaAuthInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, errGrafanaAuthInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, errGrafanaAuthInvalid
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return zero, errGrafanaAuthInvalid
	}
	var auth grafanaAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return zero, errGrafanaAuthInvalid
	}
	if auth.Email == "" || auth.Role == "" || auth.Exp <= time.Now().Unix() {
		return zero, errGrafanaAuthInvalid
	}
	return auth, nil
}
