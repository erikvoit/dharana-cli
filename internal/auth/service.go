package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/output"
)

const (
	EnvDharanaPAT = "DHARANA_ASANA_PAT"
	EnvAsanaToken = "ASANA_ACCESS_TOKEN"
)

var ErrTokenNotFound = errors.New("asana token not found")

type AsanaClient interface {
	CurrentUser(ctx context.Context, token string) (*asana.User, error)
}

type Service struct {
	Store TokenStore
	Asana AsanaClient
}

type ConfigureResult struct {
	Source    string          `json:"source"`
	Token     MaskedToken     `json:"token"`
	Validated bool            `json:"validated"`
	User      *ConfiguredUser `json:"user,omitempty"`
}

type StatusResult struct {
	Configured bool         `json:"configured"`
	Source     string       `json:"source,omitempty"`
	Token      *MaskedToken `json:"token,omitempty"`
}

type ValidateResult struct {
	Source string         `json:"source"`
	Token  MaskedToken    `json:"token"`
	User   ConfiguredUser `json:"user"`
}

type MaskedToken struct {
	Masked string `json:"masked"`
}

type ConfiguredUser struct {
	GID   string `json:"gid"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type ResolvedToken struct {
	Token  string
	Source string
}

func NewService() *Service {
	return &Service{
		Store: NewKeychainStore(),
		Asana: asana.NewClient(""),
	}
}

func (s *Service) Configure(ctx context.Context, token string, validate bool) (*ConfigureResult, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, output.NewError("MISSING_TOKEN", "Provide an Asana personal access token with --token or through stdin.")
	}

	if s.Store == nil {
		return nil, output.NewError("KEYCHAIN_UNAVAILABLE", "No token store is configured.")
	}
	if err := s.Store.Save(token); err != nil {
		return nil, output.NewError("KEYCHAIN_WRITE_FAILED", "Could not store the Asana token in the operating-system keychain.")
	}

	result := &ConfigureResult{
		Source: "keychain",
		Token:  MaskedToken{Masked: MaskToken(token)},
	}

	if validate {
		user, err := s.validateToken(ctx, token)
		if err != nil {
			return nil, err
		}
		result.Validated = true
		result.User = toConfiguredUser(user)
	}

	return result, nil
}

func (s *Service) Status() (*StatusResult, error) {
	resolved, err := s.ResolveToken()
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return &StatusResult{Configured: false}, nil
		}
		return nil, output.NewError("TOKEN_READ_FAILED", "Could not read the configured Asana token.")
	}

	masked := MaskedToken{Masked: MaskToken(resolved.Token)}
	return &StatusResult{
		Configured: true,
		Source:     resolved.Source,
		Token:      &masked,
	}, nil
}

func (s *Service) Validate(ctx context.Context) (*ValidateResult, error) {
	resolved, err := s.ResolveToken()
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return nil, output.NewError("TOKEN_NOT_CONFIGURED", "No Asana token is configured. Set one with auth configure or an environment variable.")
		}
		return nil, output.NewError("TOKEN_READ_FAILED", "Could not read the configured Asana token.")
	}

	user, err := s.validateToken(ctx, resolved.Token)
	if err != nil {
		return nil, err
	}

	return &ValidateResult{
		Source: resolved.Source,
		Token:  MaskedToken{Masked: MaskToken(resolved.Token)},
		User:   *toConfiguredUser(user),
	}, nil
}

func (s *Service) ResolveToken() (*ResolvedToken, error) {
	if token := strings.TrimSpace(os.Getenv(EnvDharanaPAT)); token != "" {
		return &ResolvedToken{Token: token, Source: "env:" + EnvDharanaPAT}, nil
	}
	if token := strings.TrimSpace(os.Getenv(EnvAsanaToken)); token != "" {
		return &ResolvedToken{Token: token, Source: "env:" + EnvAsanaToken}, nil
	}
	if s.Store == nil {
		return nil, ErrTokenNotFound
	}
	token, err := s.Store.Load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, ErrTokenNotFound
	}
	return &ResolvedToken{Token: token, Source: "keychain"}, nil
}

func (s *Service) validateToken(ctx context.Context, token string) (*asana.User, error) {
	if s.Asana == nil {
		return nil, output.NewError("ASANA_CLIENT_UNAVAILABLE", "No Asana client is configured.")
	}

	user, err := s.Asana.CurrentUser(ctx, token)
	if err == nil {
		return user, nil
	}

	var apiErr *asana.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		return nil, output.NewError("INVALID_AUTH", "Asana rejected the configured token.")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return nil, output.NewError("INVALID_AUTH", "The configured token does not have access to this Asana resource.")
	}
	return nil, output.NewError("ASANA_REQUEST_FAILED", "Could not validate the Asana token with the Asana API.")
}

func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return strings.Repeat("*", len(token))
	}
	return token[:4] + strings.Repeat("*", len(token)-8) + token[len(token)-4:]
}

func toConfiguredUser(user *asana.User) *ConfiguredUser {
	if user == nil {
		return nil
	}
	return &ConfiguredUser{
		GID:   user.GID,
		Name:  user.Name,
		Email: user.Email,
	}
}
