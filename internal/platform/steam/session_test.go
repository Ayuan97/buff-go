package steam

import (
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
