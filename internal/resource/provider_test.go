package resource

import "testing"

func TestProxyProviderValidate(t *testing.T) {
	ok := ProxyProvider{
		ID: 1, Name: "alpha", Enabled: true, Priority: 1,
		Regions: []NodeRegion{NodeRegionForeign}, HasCredential: true, Revision: 1,
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Regions = nil
	if err := bad.Validate(); err == nil {
		t.Fatal("empty regions accepted")
	}
	dup := ok
	dup.Regions = []NodeRegion{NodeRegionForeign, NodeRegionForeign}
	if err := dup.Validate(); err == nil {
		t.Fatal("duplicate regions accepted")
	}
	zero := ok
	zero.Priority = 0
	if err := zero.Validate(); err == nil {
		t.Fatal("priority 0 accepted")
	}
}

func TestRegionWatermarkValidate(t *testing.T) {
	if err := (RegionWatermark{Region: NodeRegionDomestic, Revision: 1}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (RegionWatermark{Region: NodeRegionDomestic, MinUsable: -1, Revision: 1}).Validate(); err == nil {
		t.Fatal("negative min accepted")
	}
}
