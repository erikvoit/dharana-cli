package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/output"
)

const (
	EnvOAuthClientID     = "DHARANA_ASANA_OAUTH_CLIENT_ID"
	EnvOAuthClientSecret = "DHARANA_ASANA_OAUTH_CLIENT_SECRET"
	EnvOAuthRedirectURI  = "DHARANA_ASANA_OAUTH_REDIRECT_URI"
	DefaultAuthorizeURL  = "https://app.asana.com/-/oauth_authorize"
	DefaultTokenURL      = "https://app.asana.com/-/oauth_token"
	DefaultRevokeURL     = "https://app.asana.com/-/oauth_revoke"
	DefaultTokenInfoURL  = "https://app.asana.com/-/token_info"
)

type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AuthorizeURL string
	TokenURL     string
	RevokeURL    string
	TokenInfoURL string
}

type OAuthClient struct {
	Config OAuthConfig
	HTTP   *http.Client
	Now    func() time.Time
}

type Authorization struct {
	URL      string
	State    string
	Verifier string
	Scopes   []string
}

type OAuthToken struct {
	AccessToken  string         `json:"access_token"`
	RefreshToken string         `json:"refresh_token"`
	ExpiresIn    int            `json:"expires_in"`
	TokenType    string         `json:"token_type"`
	Data         ConfiguredUser `json:"data"`
}

type TokenInfo struct {
	TokenType string `json:"token_type"`
	ExpiresIn int    `json:"expires_in"`
	ExpiresAt int64  `json:"exp"`
	Scope     string `json:"scope"`
	Active    bool   `json:"active"`
	ClientID  string `json:"client_id"`
}

func OAuthConfigFromEnv() OAuthConfig {
	return OAuthConfig{ClientID: os.Getenv(EnvOAuthClientID), ClientSecret: os.Getenv(EnvOAuthClientSecret), RedirectURI: os.Getenv(EnvOAuthRedirectURI), AuthorizeURL: DefaultAuthorizeURL, TokenURL: DefaultTokenURL, RevokeURL: DefaultRevokeURL, TokenInfoURL: DefaultTokenInfoURL}
}

func (c *OAuthClient) Begin(scopes []string) (*Authorization, error) {
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return nil, output.NewError("OAUTH_RANDOM_FAILED", "Could not create secure OAuth state.")
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		return nil, output.NewError("OAUTH_RANDOM_FAILED", "Could not create a secure PKCE verifier.")
	}
	digest := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {cfg.ClientID}, "redirect_uri": {cfg.RedirectURI}, "response_type": {"code"}, "state": {state}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}}
	scopes = normalizeScopes(scopes)
	if len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	return &Authorization{URL: cfg.AuthorizeURL + "?" + query.Encode(), State: state, Verifier: verifier, Scopes: scopes}, nil
}

func (c *OAuthClient) Exchange(ctx context.Context, code, verifier string) (*OAuthToken, error) {
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	return c.tokenRequest(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "redirect_uri": {cfg.RedirectURI}, "code": {code}, "code_verifier": {verifier}}, "OAUTH_TOKEN_EXCHANGE_FAILED")
}

func (c *OAuthClient) Refresh(ctx context.Context, refreshToken string) (*OAuthToken, error) {
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	return c.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "refresh_token": {refreshToken}}, "OAUTH_REFRESH_FAILED")
}

func (c *OAuthClient) Revoke(ctx context.Context, refreshToken string) error {
	cfg, err := c.config()
	if err != nil {
		return err
	}
	response, err := c.http().PostForm(cfg.RevokeURL, url.Values{"client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "token": {refreshToken}})
	if err != nil {
		return output.NewError("OAUTH_REVOKE_NETWORK_FAILED", "Could not reach Asana to revoke authorization.")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return output.NewError("OAUTH_REVOKE_FAILED", "Asana did not accept the authorization revocation request.")
	}
	return nil
}

func (c *OAuthClient) Introspect(ctx context.Context, token string) (*TokenInfo, error) {
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenInfoURL, strings.NewReader(url.Values{"token": {token}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http().Do(request)
	if err != nil {
		return nil, output.NewError("OAUTH_INTROSPECTION_FAILED", "Could not inspect the effective OAuth scopes.")
	}
	defer response.Body.Close()
	var info TokenInfo
	if response.StatusCode < 200 || response.StatusCode >= 300 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&info) != nil {
		return nil, output.NewError("OAUTH_INTROSPECTION_FAILED", "Asana did not return usable OAuth token information.")
	}
	return &info, nil
}

func (c *OAuthClient) tokenRequest(ctx context.Context, values url.Values, code string) (*OAuthToken, error) {
	cfg, _ := c.config()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http().Do(request)
	if err != nil {
		if code == "OAUTH_REFRESH_FAILED" {
			code = "OAUTH_REFRESH_NETWORK_FAILED"
		}
		return nil, output.NewError(code, "Could not reach Asana's OAuth token endpoint.")
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return nil, output.NewError(code, "Could not read Asana's OAuth token response.")
	}
	var token OAuthToken
	decodeErr := json.Unmarshal(body, &token)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if code == "OAUTH_REFRESH_FAILED" {
			var oauthErr struct {
				Error       string `json:"error"`
				Description string `json:"error_description"`
			}
			_ = json.Unmarshal(body, &oauthErr)
			switch {
			case response.StatusCode == http.StatusUnauthorized:
				code = "OAUTH_CLIENT_INVALID"
			case oauthErr.Error == "invalid_grant" && strings.Contains(strings.ToLower(oauthErr.Description), "expir"):
				code = "OAUTH_AUTHORIZATION_EXPIRED"
			case oauthErr.Error == "invalid_grant":
				code = "OAUTH_AUTHORIZATION_REVOKED"
			}
		}
		return nil, output.NewError(code, "Asana rejected the OAuth token request.")
	}
	if decodeErr != nil || token.AccessToken == "" {
		return nil, output.NewError(code, "Asana did not return a usable OAuth credential.")
	}
	return &token, nil
}

func (c *OAuthClient) config() (OAuthConfig, error) {
	cfg := c.Config
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = DefaultAuthorizeURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = DefaultTokenURL
	}
	if cfg.RevokeURL == "" {
		cfg.RevokeURL = DefaultRevokeURL
	}
	if cfg.TokenInfoURL == "" {
		cfg.TokenInfoURL = DefaultTokenInfoURL
	}
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" || strings.TrimSpace(cfg.RedirectURI) == "" {
		return OAuthConfig{}, output.NewError("OAUTH_CLIENT_NOT_CONFIGURED", fmt.Sprintf("Set %s, %s, and %s.", EnvOAuthClientID, EnvOAuthClientSecret, EnvOAuthRedirectURI))
	}
	return cfg, nil
}

func (c *OAuthClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}
func (c *OAuthClient) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func randomURLSafe(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func oauthErrorCode(err error, fallback string) string {
	var appErr *output.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return fallback
}
