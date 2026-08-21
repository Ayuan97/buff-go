package postgres

import (
	"errors"
	"strings"
	"testing"

	"buff-go/internal/resource"
)

func TestPrepareNodeConnectionValidatesProxyURL(t *testing.T) {
	t.Parallel()

	valid := resource.NodeConnectionInput{
		Kind:            resource.NodeKindProxy,
		Region:          resource.NodeRegionForeign,
		EgressMode:      resource.EgressModeStatic,
		ProxyCredential: []byte("socks5h://user:pass@proxy.example:1080"),
	}
	if _, err := prepareNodeConnection("valid-proxy", valid); err != nil {
		t.Fatalf("prepareNodeConnection() error = %v", err)
	}

	invalid := valid
	invalid.ProxyCredential = []byte("ftp://user:synthetic-secret@proxy.example:21")
	_, err := prepareNodeConnection("invalid-proxy", invalid)
	if !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("prepareNodeConnection() error = %v, want %v", err, ErrInvalidResource)
	}
	if err != nil && strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("prepareNodeConnection() leaked proxy credential: %v", err)
	}
}
