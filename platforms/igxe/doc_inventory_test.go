package igxedocs

import (
	"os"
	"strings"
	"testing"
)

func TestIGXEDocsDoNotClaimAdapterReady(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "不写生产适配器") {
		t.Fatal("IGXE README must keep the adapter gate")
	}
	if !strings.Contains(text, "11D") {
		t.Fatal("IGXE README must record Goal 11D as not started")
	}
}
