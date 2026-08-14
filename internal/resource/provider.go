package resource

import "fmt"

// ProviderID is the stable identity of one proxy vendor.
type ProviderID int64

// Validate rejects a missing provider identity.
func (id ProviderID) Validate() error {
	if id < 1 {
		return fmt.Errorf("provider_id must be at least 1")
	}
	return nil
}

// ProxyProvider is the credential-safe read model of one vendor.
type ProxyProvider struct {
	ID            ProviderID
	Name          string
	Enabled       bool
	Priority      int
	Regions       []NodeRegion
	HasCredential bool
	Revision      int64
}

// Validate checks persisted vendor facts without reading the credential.
func (provider ProxyProvider) Validate() error {
	if err := provider.ID.Validate(); err != nil {
		return err
	}
	if err := validateLabel("provider name", provider.Name); err != nil {
		return err
	}
	if provider.Priority < 1 || provider.Priority > 10000 {
		return fmt.Errorf("priority must be between 1 and 10000")
	}
	if err := validateProviderRegions(provider.Regions); err != nil {
		return err
	}
	if !provider.HasCredential {
		return fmt.Errorf("provider credential is required")
	}
	if provider.Revision < 1 {
		return fmt.Errorf("revision must be at least 1")
	}
	return nil
}

// RegionWatermark is one region's short-pool minimum and current usable count.
type RegionWatermark struct {
	Region    NodeRegion
	MinUsable int
	Usable    int
	Revision  int64
}

// Validate checks one watermark row.
func (mark RegionWatermark) Validate() error {
	if err := mark.Region.Validate(); err != nil {
		return err
	}
	if mark.MinUsable < 0 {
		return fmt.Errorf("min_usable must not be negative")
	}
	if mark.Usable < 0 {
		return fmt.Errorf("usable must not be negative")
	}
	if mark.Revision < 1 {
		return fmt.Errorf("revision must be at least 1")
	}
	return nil
}

func validateProviderRegions(regions []NodeRegion) error {
	if len(regions) == 0 {
		return fmt.Errorf("provider regions are required")
	}
	seen := make(map[NodeRegion]struct{}, len(regions))
	for _, region := range regions {
		if err := region.Validate(); err != nil {
			return err
		}
		if _, exists := seen[region]; exists {
			return fmt.Errorf("provider regions must be unique")
		}
		seen[region] = struct{}{}
	}
	return nil
}
