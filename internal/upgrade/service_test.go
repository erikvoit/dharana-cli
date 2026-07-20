package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckSupportsOnlineAndOfflineModes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.6.0","html_url":"https://example.test/release"}`))
	}))
	defer server.Close()
	service := &Service{ReleaseURL: server.URL}
	online := service.Check(context.Background(), "0.5.0", false)
	if online.UpdateAvailable == nil || !*online.UpdateAvailable || online.LatestVersion != "0.6.0" {
		t.Fatalf("unexpected online result %#v", online)
	}
	offline := service.Check(context.Background(), "0.5.0", true)
	if offline.UpdateAvailable != nil || !offline.Offline {
		t.Fatalf("unexpected offline result %#v", offline)
	}
}
