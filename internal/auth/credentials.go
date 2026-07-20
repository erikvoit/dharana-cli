package auth

import (
	"encoding/json"
	"errors"
	"strings"
)

const profileKeychainService = "dharana-cli/asana-auth"

type Credential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

type CredentialStore interface {
	SaveCredential(profile string, credential Credential) error
	LoadCredential(profile string) (Credential, error)
	DeleteCredential(profile string) error
}

type KeychainCredentialStore struct{ Service string }

func NewKeychainCredentialStore() *KeychainCredentialStore {
	return &KeychainCredentialStore{Service: profileKeychainService}
}

func (s *KeychainCredentialStore) SaveCredential(profile string, credential Credential) error {
	data, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	return (&KeychainStore{Service: s.service(), Account: profile}).Save(string(data))
}

func (s *KeychainCredentialStore) LoadCredential(profile string) (Credential, error) {
	value, err := (&KeychainStore{Service: s.service(), Account: profile}).Load()
	if err != nil {
		return Credential{}, err
	}
	var credential Credential
	if err := json.Unmarshal([]byte(value), &credential); err != nil {
		return Credential{}, errors.New("stored profile credential is invalid")
	}
	if strings.TrimSpace(credential.AccessToken) == "" {
		return Credential{}, ErrTokenNotFound
	}
	return credential, nil
}

func (s *KeychainCredentialStore) DeleteCredential(profile string) error {
	return (&KeychainStore{Service: s.service(), Account: profile}).Delete()
}

func (s *KeychainCredentialStore) service() string {
	if s != nil && s.Service != "" {
		return s.Service
	}
	return profileKeychainService
}
