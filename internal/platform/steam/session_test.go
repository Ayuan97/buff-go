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
