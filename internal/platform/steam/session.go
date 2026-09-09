package steam

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"buff-go/internal/resource"
)

type chromeCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

// SessionOpener loads the occupied account cookie and optional proxy URL.
type SessionOpener interface {
	Open(ctx context.Context, lease resource.Lease) (cookie string, proxy string, err error)
}

// NewCoordinatorOpener opens occupied Steam cookies and proxy URLs from a lease.
func NewCoordinatorOpener(coordinator *resource.Coordinator) SessionOpener {
	return coordinatorOpener{coordinator: coordinator}
}

type coordinatorOpener struct {
	coordinator *resource.Coordinator
}

type coordinatorRecorder struct {
	coordinator *resource.Coordinator
	now         func() time.Time
}

// NewCoordinatorRecorder writes valid/invalid session checks through a live lease.
func NewCoordinatorRecorder(coordinator *resource.Coordinator) SessionRecorder {
	return coordinatorRecorder{coordinator: coordinator, now: time.Now}
}

func (r coordinatorRecorder) Record(ctx context.Context, lease resource.Lease, valid bool) error {
	if r.coordinator == nil {
		return fmt.Errorf("steam session recorder is missing coordinator")
	}
	state := resource.AccountSessionStateValid
	if !valid {
		state = resource.AccountSessionStateInvalid
	}
	clock := r.now
	if clock == nil {
		clock = time.Now
	}
	_, err := r.coordinator.RecordAccountSessionCheck(ctx, lease.Token, state, clock().UTC())
	return err
}

func (o coordinatorOpener) Open(ctx context.Context, lease resource.Lease) (string, string, error) {
	plaintext, err := o.coordinator.OpenAccountSession(ctx, lease.Token)
	if err != nil {
		return "", "", err
	}
	cookie, err := cookieHeader(plaintext)
	if err != nil {
		return "", "", err
	}
	snapshot, err := lease.CombinationSnapshot()
	if err != nil {
		return "", "", err
	}
	if snapshot.NodeKind != resource.NodeKindProxy {
		return cookie, "", nil
	}
	proxyPlain, err := o.coordinator.OpenNodeProxyCredential(ctx, lease.Token)
	if err != nil {
		return "", "", err
	}
	return cookie, strings.TrimSpace(string(proxyPlain)), nil
}

func cookieHeader(raw []byte) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", fmt.Errorf("empty steam session")
	}
	if trimmed[0] == '[' {
		var cookies []chromeCookie
		if err := json.Unmarshal(trimmed, &cookies); err != nil {
			return "", fmt.Errorf("steam cookie json: %w", err)
		}
		parts := make([]string, 0, len(cookies))
		for _, cookie := range cookies {
			if cookie.Name == "" || cookie.Value == "" {
				continue
			}
			if cookie.Domain != "" && !strings.Contains(cookie.Domain, "steamcommunity.com") {
				continue
			}
			parts = append(parts, cookie.Name+"="+cookie.Value)
		}
		if len(parts) == 0 {
			return "", fmt.Errorf("steam cookie json has no steamcommunity cookies")
		}
		return strings.Join(parts, "; "), nil
	}
	return string(trimmed), nil
}

func newHTTPClient(proxy string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy == "" {
		// 直连必须走本机出口。Clone() 会带上 ProxyFromEnvironment，会把直连节点
		// 偷偷送进 HTTP_PROXY，出口证据和实际出口就对不上了。
		transport.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
	} else {
		if err := resource.ValidateProxyCredential([]byte(proxy)); err != nil {
			return nil, fmt.Errorf("proxy URL: %w", err)
		}
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("proxy URL is invalid")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{
		Timeout:   0,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}
