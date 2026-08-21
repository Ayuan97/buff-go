package steam

import "context"

// ItemDescriptionLine is one localized text or BBCode description entry.
type ItemDescriptionLine struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// ItemAction is an inspect or market action associated with an item.
type ItemAction struct {
	Link string `json:"link"`
	Name string `json:"name"`
}

// ItemTag is one Steam category/tag pair.
type ItemTag struct {
	Category              string `json:"category"`
	InternalName          string `json:"internal_name"`
	LocalizedCategoryName string `json:"localized_category_name"`
	LocalizedTagName      string `json:"localized_tag_name"`
	Color                 string `json:"color"`
}

// ItemDescription contains identity, presentation, and market grouping metadata.
type ItemDescription struct {
	AppID                 int64                 `json:"appid"`
	ClassID               string                `json:"classid"`
	InstanceID            string                `json:"instanceid"`
	BackgroundColor       string                `json:"background_color"`
	IconURL               string                `json:"icon_url"`
	IconURLLarge          string                `json:"icon_url_large"`
	Descriptions          []ItemDescriptionLine `json:"descriptions"`
	OwnerDescriptions     []ItemDescriptionLine `json:"owner_descriptions"`
	Actions               []ItemAction          `json:"actions"`
	OwnerActions          []ItemAction          `json:"owner_actions"`
	MarketActions         []ItemAction          `json:"market_actions"`
	Name                  string                `json:"name"`
	NameColor             string                `json:"name_color"`
	Type                  string                `json:"type"`
	MarketName            string                `json:"market_name"`
	MarketHashName        string                `json:"market_hash_name"`
	MarketBucketGroupName string                `json:"market_bucket_group_name"`
	MarketBucketGroupID   string                `json:"market_bucket_group_id"`
	MarketBucketID        string                `json:"market_bucket_id"`
	Tradable              bool                  `json:"tradable"`
	Marketable            bool                  `json:"marketable"`
	Commodity             bool                  `json:"commodity"`
	Tags                  []ItemTag             `json:"tags"`
}

// ItemDescription reads the QueryDescription action for one exact market item.
func (c *Client) ItemDescription(ctx context.Context, appID int64, marketHashName string) (ItemDescription, error) {
	if err := validateItemKey(appID, marketHashName); err != nil {
		return ItemDescription{}, err
	}
	body, err := c.queryAction(ctx, itemDescriptionAction, appID, marketHashName)
	if err != nil {
		return ItemDescription{}, err
	}
	return decodeQueryAction[ItemDescription](body, itemDescriptionAction)
}
