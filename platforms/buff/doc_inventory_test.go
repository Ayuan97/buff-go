package buffdocs

import (
	"os"
	"strings"
	"testing"
)

func TestBuffDocsDoNotClaimAdapterReady(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "不写生产适配器") {
		t.Fatal("BUFF README must keep the adapter gate")
	}
	if !strings.Contains(text, "限流") {
		t.Fatal("BUFF README must record rate-limit as unverified")
	}
}
