package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/erikvoit/dharana-cli/internal/asana"
)

type memoryProfiles struct{ state *ProfileState }

func (m *memoryProfiles) Load() (*ProfileState, error) {
	if m.state == nil {
		m.state = &ProfileState{SchemaVersion: ProfileSchemaVersion, Profiles: []Profile{}}
	}
	data, _ := json.Marshal(m.state)
	var copyValue ProfileState
	_ = json.Unmarshal(data, &copyValue)
	return &copyValue, nil
}
func (m *memoryProfiles) Save(value *ProfileState) error {
	data, _ := json.Marshal(value)
	var copyValue ProfileState
	_ = json.Unmarshal(data, &copyValue)
	m.state = &copyValue
	return nil
}

type memoryCredentials struct{ values map[string]Credential }

func (m *memoryCredentials) SaveCredential(name string, value Credential) error {
	if m.values == nil {
		m.values = map[string]Credential{}
	}
	m.values[name] = value
	return nil
}
func (m *memoryCredentials) LoadCredential(name string) (Credential, error) {
	value, ok := m.values[name]
	if !ok {
		return Credential{}, ErrTokenNotFound
	}
	return value, nil
}
func (m *memoryCredentials) DeleteCredential(name string) error {
	if _, ok := m.values[name]; !ok {
		return ErrTokenNotFound
	}
	delete(m.values, name)
	return nil
}

type oauthAsana struct{}

func (oauthAsana) CurrentUser(_ context.Context, token string) (*asana.User, error) {
	if token == "" {
		return nil, errors.New("missing")
	}
	return &asana.User{GID: "u1", Name: "Ada", Email: "ada@example.com"}, nil
}

func TestOAuthBeginUsesPKCEAndExplicitScopes(t *testing.T) {
	client := &OAuthClient{Config: OAuthConfig{ClientID: "client", ClientSecret: "secret", RedirectURI: "http://127.0.0.1:8787/callback", AuthorizeURL: "https://example.test/authorize?audience=api"}}
	authorization, err := client.Begin([]string{"tasks:write", "tasks:read", "tasks:read"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorization.URL)
	query := parsed.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" || query.Get("code_challenge") == authorization.Verifier {
		t.Fatalf("invalid PKCE authorization: %#v", query)
	}
	if query.Get("state") != authorization.State || query.Get("scope") != "tasks:read tasks:write" {
		t.Fatalf("unexpected authorization query: %#v", query)
	}
	if query.Get("audience") != "api" {
		t.Fatalf("existing authorization query was not preserved: %#v", query)
	}
}

func TestOAuthRejectsMalformedEndpointURLs(t *testing.T) {
	config := OAuthConfig{ClientID: "client", ClientSecret: "secret", RedirectURI: "http://127.0.0.1/callback"}

	config.AuthorizeURL = "https://example.test/%"
	if _, err := (&OAuthClient{Config: config}).Begin(nil); oauthErrorCode(err, "") != "OAUTH_AUTHORIZE_URL_INVALID" {
		t.Fatalf("expected invalid authorization URL error, got %v", err)
	}

	config.AuthorizeURL = ""
	config.TokenInfoURL = "://invalid"
	if _, err := (&OAuthClient{Config: config}).Introspect(context.Background(), "token"); oauthErrorCode(err, "") != "OAUTH_INTROSPECTION_FAILED" {
		t.Fatalf("expected introspection construction error, got %v", err)
	}

	config.TokenInfoURL = ""
	config.TokenURL = "://invalid"
	if _, err := (&OAuthClient{Config: config}).Refresh(context.Background(), "refresh"); oauthErrorCode(err, "") != "OAUTH_REFRESH_FAILED" {
		t.Fatalf("expected token construction error, got %v", err)
	}
}

func TestCompleteLoginAndAutomaticRefreshKeepSecretsOutOfMetadata(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var refreshCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshCalls++
				_, _ = w.Write([]byte(`{"access_token":"refreshed-access","refresh_token":"rotated-refresh","expires_in":3600,"token_type":"bearer"}`))
				return
			}
			if r.Form.Get("code_verifier") != "verifier" {
				t.Errorf("missing verifier")
			}
			_, _ = w.Write([]byte(`{"access_token":"initial-access","refresh_token":"initial-refresh","expires_in":1,"token_type":"bearer"}`))
		case "/info":
			_, _ = w.Write([]byte(`{"active":true,"scope":"tasks:read tasks:write","exp":9999999999}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	profiles := &memoryProfiles{}
	credentials := &memoryCredentials{}
	service := &Service{Profiles: profiles, Credentials: credentials, Asana: oauthAsana{}, OAuth: &OAuthClient{Config: OAuthConfig{ClientID: "client", ClientSecret: "secret", RedirectURI: "http://127.0.0.1/callback", TokenURL: server.URL + "/token", TokenInfoURL: server.URL + "/info"}}}
	result, err := service.CompleteLogin(context.Background(), "work", "code", &Authorization{Verifier: "verifier", Scopes: []string{"tasks:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile.User.Email != "ada@example.com" || !result.Profile.ScopeKnown {
		t.Fatalf("unexpected profile: %#v", result.Profile)
	}
	metadata, _ := json.Marshal(profiles.state)
	if strings.Contains(string(metadata), "initial-access") || strings.Contains(string(metadata), "initial-refresh") {
		t.Fatalf("secret leaked into metadata: %s", metadata)
	}
	service.SelectedProfile = "work"
	resolved, err := service.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Token != "refreshed-access" || refreshCalls != 1 {
		t.Fatalf("expected automatic refresh, got %#v calls=%d", resolved, refreshCalls)
	}
	if credentials.values["work"].RefreshToken != "rotated-refresh" {
		t.Fatal("expected atomic refresh-token rotation")
	}
}

func TestExplicitProfilePrecedesEnvironmentAndScopesAreEnforced(t *testing.T) {
	t.Setenv(EnvDharanaPAT, "environment-token")
	profiles := &memoryProfiles{state: &ProfileState{SchemaVersion: ProfileSchemaVersion, Active: "work", Profiles: []Profile{{Name: "work", Provider: ProviderOAuth, ScopeKnown: true, Scopes: []string{"tasks:read"}, ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}}
	credentials := &memoryCredentials{values: map[string]Credential{"work": {AccessToken: "profile-token", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}
	service := &Service{Profiles: profiles, Credentials: credentials, SelectedProfile: "work"}
	resolved, err := service.Resolve(context.Background())
	if err != nil || resolved.Token != "profile-token" {
		t.Fatalf("profile did not override environment: %#v err=%v", resolved, err)
	}
	if err := service.RequireScopes(context.Background(), []string{"tasks:read"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RequireScopes(context.Background(), []string{"tasks:write"}); err == nil {
		t.Fatal("expected missing-scope error")
	}
}

func TestOAuthStatusAndValidationNeverReturnTokenMaterial(t *testing.T) {
	profiles := &memoryProfiles{state: &ProfileState{SchemaVersion: ProfileSchemaVersion, Active: "work", Profiles: []Profile{{Name: "work", Provider: ProviderOAuth, ScopeKnown: true, Scopes: []string{"users:read"}, ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}}
	credentials := &memoryCredentials{values: map[string]Credential{"work": {AccessToken: "oauth-secret-access", RefreshToken: "oauth-secret-refresh", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}
	service := &Service{Profiles: profiles, Credentials: credentials, SelectedProfile: "work", Asana: oauthAsana{}}
	status, err := service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Token != nil {
		t.Fatalf("OAuth status exposed token material: %#v", status.Token)
	}
	validated, err := service.Validate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if validated.Token != nil {
		t.Fatalf("OAuth validation exposed token material: %#v", validated.Token)
	}
	encoded, _ := json.Marshal([]any{status, validated})
	if strings.Contains(string(encoded), "oauth-secret") {
		t.Fatalf("OAuth secret leaked in output: %s", encoded)
	}
}

func TestRefreshDistinguishesRevokedAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"authorization was revoked"}`))
	}))
	defer server.Close()
	client := &OAuthClient{Config: OAuthConfig{ClientID: "client", ClientSecret: "secret", RedirectURI: "http://127.0.0.1/callback", TokenURL: server.URL}}
	_, err := client.Refresh(context.Background(), "revoked")
	var appErr interface{ Error() string }
	if err == nil || !errors.As(err, &appErr) || !strings.Contains(err.Error(), "OAuth") {
		t.Fatalf("expected stable revoked error, got %v", err)
	}
	if oauthErrorCode(err, "") != "OAUTH_AUTHORIZATION_REVOKED" {
		t.Fatalf("unexpected code %s", oauthErrorCode(err, ""))
	}
}
