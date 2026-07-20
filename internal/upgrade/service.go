package upgrade

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const DefaultReleaseURL = "https://api.github.com/repos/erikvoit/dharana-cli/releases/latest"

type Result struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable *bool  `json:"update_available,omitempty"`
	Offline         bool   `json:"offline"`
	ReleaseURL      string `json:"release_url,omitempty"`
	Message         string `json:"message"`
}
type Service struct {
	HTTP       *http.Client
	ReleaseURL string
}

func (s *Service) Check(ctx context.Context, current string, offline bool) *Result {
	result := &Result{CurrentVersion: current, Offline: offline, Message: "Upgrade checks are advisory and never block normal commands."}
	if offline {
		return result
	}
	endpoint := s.ReleaseURL
	if endpoint == "" {
		endpoint = DefaultReleaseURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		result.Message = "Could not check for releases; normal commands remain available."
		return result
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.client().Do(request)
	if err != nil {
		result.Message = "Could not check for releases; normal commands remain available."
		return result
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Message = "No stable release information is currently available."
		return result
	}
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if json.NewDecoder(response.Body).Decode(&release) != nil || release.TagName == "" {
		result.Message = "Release information was not usable."
		return result
	}
	result.LatestVersion = strings.TrimPrefix(release.TagName, "v")
	result.ReleaseURL = release.HTMLURL
	available := compareVersions(result.LatestVersion, current) > 0
	result.UpdateAvailable = &available
	return result
}
func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}
func compareVersions(a, b string) int {
	parse := func(value string) []int {
		value = strings.TrimPrefix(value, "v")
		value = strings.SplitN(value, "-", 2)[0]
		parts := strings.Split(value, ".")
		out := make([]int, 3)
		for i := 0; i < len(parts) && i < 3; i++ {
			out[i], _ = strconv.Atoi(parts[i])
		}
		return out
	}
	aa, bb := parse(a), parse(b)
	for i := range aa {
		if aa[i] > bb[i] {
			return 1
		}
		if aa[i] < bb[i] {
			return -1
		}
	}
	return 0
}
