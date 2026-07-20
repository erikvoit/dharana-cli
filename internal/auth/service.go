package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

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
	Store           TokenStore
	Asana           AsanaClient
	Profiles        ProfileStore
	Credentials     CredentialStore
	OAuth           *OAuthClient
	SelectedProfile string
	refreshMu       sync.Mutex
}

type ConfigureResult struct {
	Source     string          `json:"source"`
	Provider   Provider        `json:"provider"`
	Profile    string          `json:"profile,omitempty"`
	ScopeKnown bool            `json:"scope_known"`
	Token      MaskedToken     `json:"token"`
	Validated  bool            `json:"validated"`
	User       *ConfiguredUser `json:"user,omitempty"`
}

type StatusResult struct {
	Configured bool            `json:"configured"`
	Source     string          `json:"source,omitempty"`
	Provider   Provider        `json:"provider,omitempty"`
	Profile    string          `json:"profile,omitempty"`
	User       *ConfiguredUser `json:"user,omitempty"`
	ExpiresAt  string          `json:"expires_at,omitempty"`
	Scopes     []string        `json:"scopes,omitempty"`
	ScopeKnown bool            `json:"scope_known"`
	Token      *MaskedToken    `json:"token,omitempty"`
}

type ValidateResult struct {
	Source   string         `json:"source"`
	Token    *MaskedToken   `json:"token,omitempty"`
	Provider Provider       `json:"provider,omitempty"`
	Profile  string         `json:"profile,omitempty"`
	User     ConfiguredUser `json:"user"`
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
	Token      string
	Source     string
	Provider   Provider
	Profile    string
	User       *ConfiguredUser
	ExpiresAt  string
	Scopes     []string
	ScopeKnown bool
}

type ProfilesResult struct {
	Active   string    `json:"active_profile,omitempty"`
	Profiles []Profile `json:"profiles"`
}
type ScopeResult struct {
	Profile  string   `json:"profile,omitempty"`
	Provider Provider `json:"provider"`
	Known    bool     `json:"known"`
	Granted  []string `json:"granted,omitempty"`
}
type LogoutResult struct {
	Profile       string `json:"profile"`
	LocalRemoved  bool   `json:"local_removed"`
	RemoteRevoked bool   `json:"remote_revoked"`
	RemoteError   string `json:"remote_error,omitempty"`
}
type LoginResult struct {
	Profile         Profile  `json:"profile"`
	RequestedScopes []string `json:"requested_scopes"`
}

func NewService() *Service {
	return &Service{
		Store: NewKeychainStore(), Asana: asana.NewClient(""), Profiles: NewFileProfileStore(),
		Credentials: NewKeychainCredentialStore(), OAuth: &OAuthClient{Config: OAuthConfigFromEnv()},
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
		Source:   "keychain",
		Provider: ProviderPAT,
		Token:    MaskedToken{Masked: MaskToken(token)},
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

func (s *Service) ConfigureProfile(ctx context.Context, name, token string, validate bool) (*ConfigureResult, error) {
	name = strings.TrimSpace(name)
	token = strings.TrimSpace(token)
	if name == "" {
		return nil, output.NewError("AUTH_PROFILE_NAME_REQUIRED", "Provide a profile name.")
	}
	if token == "" {
		return nil, output.NewError("MISSING_TOKEN", "Provide an Asana personal access token with --token or through stdin.")
	}
	profile := Profile{Name: name, Provider: ProviderPAT, ScopeKnown: false}
	var user *ConfiguredUser
	if validate {
		resolvedUser, err := s.validateToken(ctx, token)
		if err != nil {
			return nil, err
		}
		user = toConfiguredUser(resolvedUser)
		profile.User = *user
	}
	credentialStore := s.credentialStore()
	previous, previousErr := credentialStore.LoadCredential(name)
	if err := credentialStore.SaveCredential(name, Credential{AccessToken: token}); err != nil {
		return nil, output.NewError("CREDENTIAL_WRITE_FAILED", "Could not store the profile credential in the operating-system credential store.")
	}
	state, err := s.profileStore().Load()
	if err != nil {
		restoreCredential(credentialStore, name, previous, previousErr)
		return nil, err
	}
	state.Upsert(profile)
	if state.Active == "" {
		state.Active = name
	}
	if err := s.profileStore().Save(state); err != nil {
		restoreCredential(credentialStore, name, previous, previousErr)
		return nil, output.NewError("AUTH_PROFILE_WRITE_FAILED", "Could not save authentication profile metadata.")
	}
	return &ConfigureResult{Source: "profile:" + name, Provider: ProviderPAT, Profile: name, ScopeKnown: false, Token: MaskedToken{Masked: MaskToken(token)}, Validated: validate, User: user}, nil
}

func (s *Service) ConfigureEnvironmentProfile(ctx context.Context, name string) (*Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, output.NewError("AUTH_PROFILE_NAME_REQUIRED", "Provide a profile name.")
	}
	token := strings.TrimSpace(os.Getenv(EnvDharanaPAT))
	if token == "" {
		token = strings.TrimSpace(os.Getenv(EnvAsanaToken))
	}
	if token == "" {
		return nil, output.NewError("TOKEN_NOT_CONFIGURED", "Set a supported Asana token environment variable before creating an environment profile.")
	}
	user, err := s.validateToken(ctx, token)
	if err != nil {
		return nil, err
	}
	profile := Profile{Name: name, Provider: ProviderEnvironment, User: *toConfiguredUser(user), ScopeKnown: false}
	state, err := s.profileStore().Load()
	if err != nil {
		return nil, err
	}
	state.Upsert(profile)
	if state.Active == "" {
		state.Active = name
	}
	if err := s.profileStore().Save(state); err != nil {
		return nil, output.NewError("AUTH_PROFILE_WRITE_FAILED", "Could not save environment profile metadata.")
	}
	return &profile, nil
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
	result := &StatusResult{
		Configured: true,
		Source:     resolved.Source,
		Provider:   resolved.Provider, Profile: resolved.Profile, User: resolved.User,
		ExpiresAt: resolved.ExpiresAt, Scopes: resolved.Scopes, ScopeKnown: resolved.ScopeKnown,
	}
	if resolved.Provider != ProviderOAuth {
		result.Token = &masked
	}
	return result, nil
}

func (s *Service) Validate(ctx context.Context) (*ValidateResult, error) {
	resolved, err := s.Resolve(ctx)
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

	result := &ValidateResult{Source: resolved.Source, Provider: resolved.Provider, Profile: resolved.Profile, User: *toConfiguredUser(user)}
	if resolved.Provider != ProviderOAuth {
		masked := MaskedToken{Masked: MaskToken(resolved.Token)}
		result.Token = &masked
	}
	return result, nil
}

func (s *Service) ResolveToken() (*ResolvedToken, error) {
	return s.Resolve(context.Background())
}

func (s *Service) Resolve(ctx context.Context) (*ResolvedToken, error) {
	if name := strings.TrimSpace(s.SelectedProfile); name != "" {
		return s.resolveProfile(ctx, name)
	}
	if token := strings.TrimSpace(os.Getenv(EnvDharanaPAT)); token != "" {
		return &ResolvedToken{Token: token, Source: "env:" + EnvDharanaPAT, Provider: ProviderEnvironment}, nil
	}
	if token := strings.TrimSpace(os.Getenv(EnvAsanaToken)); token != "" {
		return &ResolvedToken{Token: token, Source: "env:" + EnvAsanaToken, Provider: ProviderEnvironment}, nil
	}
	if state, err := s.profileStore().Load(); err == nil && state.Active != "" {
		return s.resolveProfile(ctx, state.Active)
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
	return &ResolvedToken{Token: token, Source: "keychain", Provider: ProviderPAT}, nil
}

func (s *Service) resolveProfile(ctx context.Context, name string) (*ResolvedToken, error) {
	state, err := s.profileStore().Load()
	if err != nil {
		return nil, output.NewError("AUTH_PROFILE_READ_FAILED", "Could not read authentication profile metadata.")
	}
	profile, ok := state.Profile(name)
	if !ok {
		return nil, output.NewError("AUTH_PROFILE_NOT_FOUND", "The requested authentication profile does not exist.")
	}
	if profile.Provider == ProviderEnvironment {
		token := strings.TrimSpace(os.Getenv(EnvDharanaPAT))
		source := "env:" + EnvDharanaPAT
		if token == "" {
			token = strings.TrimSpace(os.Getenv(EnvAsanaToken))
			source = "env:" + EnvAsanaToken
		}
		if token == "" {
			return nil, ErrTokenNotFound
		}
		return resolvedForProfile(token, source, *profile), nil
	}
	credential, err := s.credentialStore().LoadCredential(name)
	if err != nil {
		return nil, err
	}
	if profile.Provider == ProviderOAuth && credential.RefreshToken != "" && expiresSoon(credential.ExpiresAt, time.Now(), 5*time.Minute) {
		s.refreshMu.Lock()
		defer s.refreshMu.Unlock()
		release, lockErr := acquireRefreshLock(name)
		if lockErr != nil {
			return nil, lockErr
		}
		defer release()
		credential, err = s.credentialStore().LoadCredential(name)
		if err != nil {
			return nil, err
		}
		if expiresSoon(credential.ExpiresAt, time.Now(), 5*time.Minute) {
			credential, profile, err = s.refreshCredential(ctx, name, credential, profile)
			if err != nil {
				return nil, err
			}
		}
	}
	return resolvedForProfile(credential.AccessToken, "profile:"+name, *profile), nil
}

func resolvedForProfile(token, source string, profile Profile) *ResolvedToken {
	user := profile.User
	return &ResolvedToken{Token: token, Source: source, Provider: profile.Provider, Profile: profile.Name, User: &user, ExpiresAt: profile.ExpiresAt, Scopes: append([]string(nil), profile.Scopes...), ScopeKnown: profile.ScopeKnown}
}

func (s *Service) BeginLogin(scopes []string) (*Authorization, error) { return s.oauth().Begin(scopes) }

func (s *Service) CompleteLogin(ctx context.Context, profileName, code string, authorization *Authorization) (*LoginResult, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return nil, output.NewError("AUTH_PROFILE_NAME_REQUIRED", "Provide a profile name.")
	}
	if authorization == nil || strings.TrimSpace(code) == "" {
		return nil, output.NewError("OAUTH_CODE_REQUIRED", "The OAuth callback did not provide an authorization code.")
	}
	token, err := s.oauth().Exchange(ctx, code, authorization.Verifier)
	if err != nil {
		return nil, err
	}
	user, err := s.validateToken(ctx, token.AccessToken)
	if err != nil {
		return nil, err
	}
	info, err := s.oauth().Introspect(ctx, token.AccessToken)
	if err != nil {
		return nil, err
	}
	if !info.Active {
		return nil, output.NewError("OAUTH_TOKEN_INACTIVE", "Asana returned an inactive OAuth credential.")
	}
	scopes := normalizeScopes([]string{info.Scope})
	expiresAt := time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	credential := Credential{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: expiresAt}
	credentialStore := s.credentialStore()
	previous, previousErr := credentialStore.LoadCredential(profileName)
	if err := credentialStore.SaveCredential(profileName, credential); err != nil {
		return nil, output.NewError("CREDENTIAL_WRITE_FAILED", "Could not store OAuth credentials in the operating-system credential store.")
	}
	state, err := s.profileStore().Load()
	if err != nil {
		restoreCredential(credentialStore, profileName, previous, previousErr)
		return nil, output.NewError("AUTH_PROFILE_READ_FAILED", "Could not read authentication profiles.")
	}
	profile := Profile{Name: profileName, Provider: ProviderOAuth, User: *toConfiguredUser(user), Scopes: scopes, ScopeKnown: true, ExpiresAt: expiresAt}
	state.Upsert(profile)
	if state.Active == "" {
		state.Active = profileName
	}
	if err := s.profileStore().Save(state); err != nil {
		restoreCredential(credentialStore, profileName, previous, previousErr)
		return nil, output.NewError("AUTH_PROFILE_WRITE_FAILED", "Could not save authentication profile metadata.")
	}
	return &LoginResult{Profile: profile, RequestedScopes: authorization.Scopes}, nil
}

func (s *Service) RefreshProfile(ctx context.Context, name string) (*StatusResult, error) {
	release, lockErr := acquireRefreshLock(name)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	state, err := s.profileStore().Load()
	if err != nil {
		return nil, err
	}
	profile, ok := state.Profile(name)
	if !ok {
		return nil, output.NewError("AUTH_PROFILE_NOT_FOUND", "The requested authentication profile does not exist.")
	}
	if profile.Provider != ProviderOAuth {
		return nil, output.NewError("AUTH_REFRESH_UNSUPPORTED", "Only OAuth profiles can be refreshed.")
	}
	credential, err := s.credentialStore().LoadCredential(name)
	if err != nil {
		return nil, err
	}
	_, _, err = s.refreshCredential(ctx, name, credential, profile)
	if err != nil {
		return nil, err
	}
	previous := s.SelectedProfile
	s.SelectedProfile = name
	defer func() { s.SelectedProfile = previous }()
	return s.Status()
}

func (s *Service) refreshCredential(ctx context.Context, name string, credential Credential, profile *Profile) (Credential, *Profile, error) {
	if credential.RefreshToken == "" {
		return credential, profile, output.NewError("OAUTH_REFRESH_TOKEN_MISSING", "The OAuth profile must be authorized again.")
	}
	token, err := s.oauth().Refresh(ctx, credential.RefreshToken)
	if err != nil {
		return credential, profile, err
	}
	credential.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		credential.RefreshToken = token.RefreshToken
	}
	credential.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	if err := s.credentialStore().SaveCredential(name, credential); err != nil {
		return credential, profile, output.NewError("CREDENTIAL_WRITE_FAILED", "Could not atomically store refreshed OAuth credentials.")
	}
	state, err := s.profileStore().Load()
	if err != nil {
		return credential, profile, err
	}
	updated, ok := state.Profile(name)
	if !ok {
		return credential, profile, output.NewError("AUTH_PROFILE_NOT_FOUND", "The refreshed profile metadata no longer exists.")
	}
	updated.ExpiresAt = credential.ExpiresAt
	state.Upsert(*updated)
	if err := s.profileStore().Save(state); err != nil {
		return credential, updated, output.NewError("AUTH_PROFILE_WRITE_FAILED", "Could not update OAuth profile expiry metadata.")
	}
	return credential, updated, nil
}

func (s *Service) Logout(ctx context.Context, name string, revoke bool) (*LogoutResult, error) {
	state, err := s.profileStore().Load()
	if err != nil {
		return nil, err
	}
	profile, ok := state.Profile(name)
	if !ok {
		return nil, output.NewError("AUTH_PROFILE_NOT_FOUND", "The requested authentication profile does not exist.")
	}
	result := &LogoutResult{Profile: name}
	var credential Credential
	if profile.Provider != ProviderEnvironment {
		credential, _ = s.credentialStore().LoadCredential(name)
	}
	if profile.Provider != ProviderEnvironment {
		if err := s.credentialStore().DeleteCredential(name); err != nil && !errors.Is(err, ErrTokenNotFound) {
			return nil, output.NewError("CREDENTIAL_DELETE_FAILED", "Could not remove local credentials.")
		}
	}
	state.Remove(name)
	if err := s.profileStore().Save(state); err != nil {
		return nil, output.NewError("AUTH_PROFILE_WRITE_FAILED", "Local credentials were removed but profile metadata could not be updated.")
	}
	result.LocalRemoved = true
	if revoke && profile.Provider == ProviderOAuth && credential.RefreshToken != "" {
		if err := s.oauth().Revoke(ctx, credential.RefreshToken); err != nil {
			result.RemoteError = oauthErrorCode(err, "OAUTH_REVOKE_FAILED")
		} else {
			result.RemoteRevoked = true
		}
	}
	return result, nil
}

func (s *Service) ListProfiles() (*ProfilesResult, error) {
	state, err := s.profileStore().Load()
	if err != nil {
		return nil, err
	}
	return &ProfilesResult{Active: state.Active, Profiles: state.Profiles}, nil
}
func (s *Service) ShowProfile(name string) (*Profile, error) {
	state, err := s.profileStore().Load()
	if err != nil {
		return nil, err
	}
	p, ok := state.Profile(name)
	if !ok {
		return nil, output.NewError("AUTH_PROFILE_NOT_FOUND", "The requested authentication profile does not exist.")
	}
	copyValue := *p
	return &copyValue, nil
}
func (s *Service) UseProfile(name string) (*Profile, error) {
	state, err := s.profileStore().Load()
	if err != nil {
		return nil, err
	}
	p, ok := state.Profile(name)
	if !ok {
		return nil, output.NewError("AUTH_PROFILE_NOT_FOUND", "The requested authentication profile does not exist.")
	}
	state.Active = name
	if err := s.profileStore().Save(state); err != nil {
		return nil, err
	}
	copyValue := *p
	return &copyValue, nil
}
func (s *Service) Scopes(ctx context.Context) (*ScopeResult, error) {
	resolved, err := s.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	return &ScopeResult{Profile: resolved.Profile, Provider: resolved.Provider, Known: resolved.ScopeKnown, Granted: resolved.Scopes}, nil
}
func (s *Service) RequireScopes(ctx context.Context, required []string) error {
	resolved, err := s.Resolve(ctx)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return output.NewError("TOKEN_NOT_CONFIGURED", "No Asana token is configured. Authorize a profile or configure a PAT.")
		}
		return err
	}
	if !resolved.ScopeKnown {
		return nil
	}
	granted := map[string]bool{}
	for _, scope := range resolved.Scopes {
		granted[scope] = true
	}
	var missing []string
	for _, scope := range normalizeScopes(required) {
		if !granted[scope] {
			missing = append(missing, scope)
		}
	}
	if len(missing) > 0 {
		return output.NewErrorWithDetails("OAUTH_SCOPES_MISSING", "The active OAuth profile does not grant all scopes required by this operation.", map[string]any{"profile": resolved.Profile, "missing": missing, "reauthorize": "dharana auth login --profile " + resolved.Profile + " --scope " + strings.Join(missing, " --scope ")})
	}
	return nil
}

func (s *Service) profileStore() ProfileStore {
	if s.Profiles != nil {
		return s.Profiles
	}
	return NewFileProfileStore()
}
func (s *Service) credentialStore() CredentialStore {
	if s.Credentials != nil {
		return s.Credentials
	}
	return NewKeychainCredentialStore()
}
func (s *Service) oauth() *OAuthClient {
	if s.OAuth != nil {
		return s.OAuth
	}
	return &OAuthClient{Config: OAuthConfigFromEnv()}
}
func expiresSoon(value string, now time.Time, window time.Duration) bool {
	expires, err := time.Parse(time.RFC3339, value)
	return err == nil && !expires.After(now.Add(window))
}

func restoreCredential(store CredentialStore, name string, previous Credential, previousErr error) {
	if previousErr == nil {
		_ = store.SaveCredential(name, previous)
	} else {
		_ = store.DeleteCredential(name)
	}
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
