// Package catalog contains legacy Steam catalog import, HTTP, and PostgreSQL adapters.
package catalog

import (
	"fmt"
	"strings"
)

// Item is the legacy Steam catalog import DTO. MarketHashName remains raw
// Steam evidence until Goal 0B confirms the formal Steam identity fields.
type Item struct {
	AppID           int64  `json:"appid"`
	MarketHashName  string `json:"market_hash_name"`
	Name            string `json:"name,omitempty"`
	IconURL         string `json:"icon_url,omitempty"`
	SteamItemNameID string `json:"steam_item_name_id,omitempty"`
	ClassID         string `json:"classid,omitempty"`
	Commodity       bool   `json:"commodity,omitempty"`
}

// Normalize trims fields and fills Name from MarketHashName when empty.
// Returns an error if AppID or MarketHashName is missing after trim.
func (it *Item) Normalize() error {
	if it == nil {
		return fmt.Errorf("nil item")
	}
	it.MarketHashName = strings.TrimSpace(it.MarketHashName)
	it.Name = strings.TrimSpace(it.Name)
	it.IconURL = strings.TrimSpace(it.IconURL)
	it.SteamItemNameID = strings.TrimSpace(it.SteamItemNameID)
	it.ClassID = strings.TrimSpace(it.ClassID)
	if it.AppID <= 0 {
		return fmt.Errorf("appid is required")
	}
	if it.MarketHashName == "" {
		return fmt.Errorf("market_hash_name is required")
	}
	if it.Name == "" {
		it.Name = it.MarketHashName
	}
	return nil
}

// GameMeta is a lightweight games-row seed used when ensuring appid exists.
type GameMeta struct {
	AppID   int64
	Code    string
	Name    string
	Enabled bool
}

// KnownGame returns a default GameMeta for well-known appids; otherwise a generic code.
func KnownGame(appid int64) GameMeta {
	switch appid {
	case 252490:
		return GameMeta{AppID: 252490, Code: "rust", Name: "Rust", Enabled: true}
	case 730:
		return GameMeta{AppID: 730, Code: "cs2", Name: "Counter-Strike 2", Enabled: true}
	case 570:
		return GameMeta{AppID: 570, Code: "dota2", Name: "Dota 2", Enabled: true}
	case 440:
		return GameMeta{AppID: 440, Code: "tf2", Name: "Team Fortress 2", Enabled: true}
	default:
		return GameMeta{
			AppID:   appid,
			Code:    fmt.Sprintf("app%d", appid),
			Name:    fmt.Sprintf("App %d", appid),
			Enabled: true,
		}
	}
}
