package steam

import (
	"context"
	"fmt"
	"strings"
)

// ListingsRequest selects one page of concrete listings from a Steam market group.
type ListingsRequest struct {
	AppID   int64
	GroupID string
	Start   int
}

// AssetProperty contains a typed property attached to a concrete market asset.
type AssetProperty struct {
	PropertyID  int64    `json:"propertyid"`
	IntValue    string   `json:"int_value"`
	FloatValue  *float64 `json:"float_value"`
	StringValue string   `json:"string_value"`
}

// AssetAccessory describes a sticker, charm, or nested accessory on a market asset.
type AssetAccessory struct {
	ClassID                      string           `json:"classid"`
	StandaloneProperties         []AssetProperty  `json:"standalone_properties"`
	ParentRelationshipProperties []AssetProperty  `json:"parent_relationship_properties"`
	NestedAccessories            []AssetAccessory `json:"nested_accessories"`
	Description                  ItemDescription  `json:"description"`
}

// ListingAsset is the concrete inventory asset offered by one listing.
type ListingAsset struct {
	ID          string           `json:"id"`
	AssetID     string           `json:"assetid"`
	InstanceID  string           `json:"instanceid"`
	ClassID     string           `json:"classid"`
	Amount      int64            `json:"amount"`
	AppID       int64            `json:"appid"`
	ContextID   string           `json:"contextid"`
	Properties  []AssetProperty  `json:"asset_properties"`
	Accessories []AssetAccessory `json:"asset_accessories"`
}

// Listing is one concrete sell order with its item and fee details.
type Listing struct {
	ListingID       string          `json:"listingid"`
	Price           int64           `json:"unPrice"`
	Fee             int64           `json:"unFee"`
	Currency        int             `json:"eCurrency"`
	SubtotalText    string          `json:"strSubtotal"`
	PublisherFeeApp int64           `json:"publisherFeeApp"`
	PublisherFeePct float64         `json:"publisherFeePct"`
	Description     ItemDescription `json:"description"`
	Asset           ListingAsset    `json:"asset"`
	Mine            bool            `json:"bMine"`
}

// ListingFacet reports one tag count within a grouped listing response.
type ListingFacet struct {
	Tag      ItemTag `json:"tag"`
	Listings int64   `json:"listings"`
}

// ListingsResponse is one fixed-size page returned by QueryListingsForItem.
type ListingsResponse struct {
	More       bool           `json:"more"`
	Start      int            `json:"start"`
	TotalCount int            `json:"total_count"`
	Listings   []Listing      `json:"listings"`
	Facets     []ListingFacet `json:"facets"`
}

// Listings reads concrete non-commodity sell orders for a market group ID.
func (c *Client) Listings(ctx context.Context, request ListingsRequest) (ListingsResponse, error) {
	if request.AppID < 1 {
		return ListingsResponse{}, fmt.Errorf("steam listings appid must be positive")
	}
	if strings.TrimSpace(request.GroupID) == "" {
		return ListingsResponse{}, fmt.Errorf("steam listings group ID is required")
	}
	if request.Start < 0 {
		return ListingsResponse{}, fmt.Errorf("steam listings start cannot be negative")
	}
	params := struct {
		AppID            int64               `json:"appid"`
		ItemName         string              `json:"strItemName"`
		Filters          map[string][]string `json:"filters"`
		AccessoryFilters map[string][]string `json:"accessoryFilters"`
		PropertyFilters  map[string][]string `json:"propertyFilters"`
		Start            int                 `json:"start"`
	}{
		AppID: request.AppID, ItemName: request.GroupID, Start: request.Start,
		Filters: map[string][]string{}, AccessoryFilters: map[string][]string{},
		PropertyFilters: map[string][]string{},
	}
	body, err := c.queryAction(ctx, itemListingsAction, params)
	if err != nil {
		return ListingsResponse{}, err
	}
	return decodeQueryAction[ListingsResponse](body, itemListingsAction)
}
