package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
)

type keys struct {
	Old, Current string
	AgentTokens  [2]string
}
type publicTrust = charlie.LocalPublicTrust

func sum(value []byte) string {
	bytes := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(bytes[:])
}

func randomCredential() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func createKeys(path string) (keys, string, error) {
	var k keys
	var err error
	if k.Old, err = auth.GenerateKey(); err != nil {
		return k, "", err
	}
	if k.Current, err = auth.GenerateKey(); err != nil {
		return k, "", err
	}
	for i := range k.AgentTokens {
		if k.AgentTokens[i], err = randomCredential(); err != nil {
			return k, "", err
		}
	}
	data, _ := json.Marshal(k)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return k, "", errors.New("disposable key reference must be new")
	}
	defer func(close func() error) { _ = close() }(file.Close)
	if _, err := file.Write(data); err != nil {
		return k, "", errors.New("key reference write failed")
	}
	return k, sum(data), nil
}

func recoveredKeys(path, expectedDigest, negative string) (keys, *auth.Encryptor, error) {
	data, err := privateFile(path)
	if err != nil {
		return keys{}, nil, err
	}
	if sum(data) != expectedDigest {
		return keys{}, nil, errors.New("independently recovered key reference differs")
	}
	var k keys
	if decodeClosed(data, &k) != nil || k.Old == "" || k.Current == "" || k.AgentTokens[0] == "" || k.AgentTokens[1] == "" {
		return k, nil, errors.New("incomplete recovered keys")
	}
	ring := "w12-current:" + k.Current + ",w12-old:" + k.Old
	if negative == "missing-old" {
		ring = "w12-current:" + k.Current
	}
	if negative == "wrong" {
		wrong, err := auth.GenerateKey()
		if err != nil {
			return k, nil, err
		}
		ring = "w12-old:" + wrong + ",w12-current:" + wrong
	}
	encryptor, err := auth.NewEncryptor(ring)
	return k, encryptor, err
}
