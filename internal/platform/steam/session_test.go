package steam

import (
	"net/http"
	"strings"
	"testing"
)

func TestCookieHeaderChromeJSON(t *testing.T) {
	raw := []byte(`[
		{"name":"sessionid","value":"abc","domain":".steamcommunity.com"},
		{"name":"steamLoginSecure","value":"token","domain":"steamcommunity.com"},
		{"name":"other","value":"skip","domain":".example.com"}
	]`)
	got, err := cookieHeader(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "sessionid=abc") || !strings.Contains(got, "steamLoginSecure=token") {
		t.Fatalf("cookie=%q", got)
	}
	if strings.Contains(got, "other=") {
		t.Fatalf("foreign cookie leaked: %q", got)
	}
}

func TestCookieHeaderRawString(t *testing.T) {
	got, err := cookieHeader([]byte("steamLoginSecure=raw"))
	if err != nil || got != "steamLoginSecure=raw" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestNewHTTPClientValidatesProxyBeforeUse(t *testing.T) {
	for _, proxy := range []string{
		"http://proxy.example:8080",
		"https://proxy.example:8443",
		"socks5://proxy.example:1080",
		"socks5h://proxy.example:1080",
	} {
		client, err := newHTTPClient(proxy)
		if err != nil {
			t.Fatalf("newHTTPClient(%q) error = %v", proxy, err)
		}
		client.CloseIdleConnections()
	}

	secret := "synthetic-proxy-password"
	client, err := newHTTPClient("ftp://user:" + secret + "@proxy.example:21")
	if client != nil || err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("invalid proxy client=%v error=%v", client, err)
	}
}

func TestNewHTTPClientDirectIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://env-proxy.example:8080")
	t.Setenv("http_proxy", "http://env-proxy.example:8080")
	t.Setenv("HTTPS_PROXY", "http://env-proxy.example:8080")
	t.Setenv("https_proxy", "http://env-proxy.example:8080")
	t.Setenv("ALL_PROXY", "http://env-proxy.example:8080")
	t.Setenv("all_proxy", "http://env-proxy.example:8080")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	client, err := newHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodGet, "https://steamcommunity.com/market/", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := client.Transport.(*http.Transport).Proxy(req)
	if err != nil || proxyURL != nil {
		t.Fatalf("direct node proxy=%v err=%v", proxyURL, err)
	}
}

func TestNewHTTPClientUsesLeaseProxyNotEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://env-proxy.example:8080")
	t.Setenv("http_proxy", "http://env-proxy.example:8080")
	t.Setenv("HTTPS_PROXY", "http://env-proxy.example:8080")
	t.Setenv("https_proxy", "http://env-proxy.example:8080")

	client, err := newHTTPClient("http://lease-proxy.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodGet, "https://steamcommunity.com/market/", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := client.Transport.(*http.Transport).Proxy(req)
	if err != nil || proxyURL == nil || proxyURL.Host != "lease-proxy.example:8080" {
		t.Fatalf("lease proxy=%v err=%v", proxyURL, err)
	}
}
