package auth

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

const (
	keychainService = "dharana-cli/asana-pat"
	keychainAccount = "asana"
)

type TokenStore interface {
	Save(token string) error
	Load() (string, error)
	Delete() error
}

type KeychainStore struct {
	Service string
	Account string
}

func NewKeychainStore() *KeychainStore {
	return &KeychainStore{
		Service: keychainService,
		Account: keychainAccount,
	}
}

func (s *KeychainStore) Save(token string) error {
	return runSecurity(
		"add-generic-password",
		"-a", s.account(),
		"-s", s.service(),
		"-w", token,
		"-U",
	)
}

func (s *KeychainStore) Load() (string, error) {
	var stdout bytes.Buffer
	cmd := exec.Command("security", "find-generic-password", "-a", s.account(), "-s", s.service(), "-w")
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "could not be found") {
			return "", ErrTokenNotFound
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (s *KeychainStore) Delete() error {
	err := runSecurity("delete-generic-password", "-a", s.account(), "-s", s.service())
	if err != nil {
		return err
	}
	return nil
}

func (s *KeychainStore) service() string {
	if s.Service == "" {
		return keychainService
	}
	return s.Service
}

func (s *KeychainStore) account() string {
	if s.Account == "" {
		return keychainAccount
	}
	return s.Account
}

func runSecurity(args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.Command("security", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return errors.New(msg)
	}
	return nil
}
