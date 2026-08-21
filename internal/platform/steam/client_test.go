package steam

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientPriceOverview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != priceOverviewPath {
			t.Fatalf("path=%q", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("appid") != "730" || query.Get("market_hash_name") != "Revolution Case" ||
			query.Get("country") != "CN" || query.Get("currency") != "23" {
			t.Fatalf("query=%v", query)
		}
		_, _ = io.WriteString(w, `{"success":true,"lowest_price":"¥ 2.45","median_price":"¥ 2.40","volume":"10,000"}`)
	}))
	t.Cleanup(server.Close)
	client := mustClient(t, server)
	result, err := client.PriceOverview(context.Background(), PriceOverviewRequest{
		AppID: 730, MarketHashName: "Revolution Case", Country: "CN", Currency: 23,
	})
	if err != nil || !result.Success || result.LowestPrice != "¥ 2.45" || result.Volume != "10,000" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClientPriceOverviewRejectsNull(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `null`)
	}))
	t.Cleanup(server.Close)
	_, err := mustClient(t, server).PriceOverview(context.Background(), PriceOverviewRequest{
		AppID: 730, MarketHashName: "Revolution Case", Currency: 1,
	})
	if err == nil {
		t.Fatal("null priceoverview must fail")
	}
}

func TestClientQueryActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != marketActionsPath {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if r.Header.Get(queryActionHeader) != queryActionHeaderVal {
			t.Fatalf("%s=%q", queryActionHeader, r.Header.Get(queryActionHeader))
		}
		action := r.URL.Query().Get("q")
		var params []json.RawMessage
		if err := json.Unmarshal([]byte(r.URL.Query().Get("qp")), &params); err != nil {
			t.Fatalf("qp=%q err=%v", r.URL.Query().Get("qp"), err)
		}
		switch action {
		case "QueryPriceHistory":
			assertItemParams(t, params, "Revolution Case")
			_, _ = io.WriteString(w, `{"data":{"ecurrency":23,"prices":[{"time":1,"price_median":2.5,"purchases":3}]}}`)
		case "QueryDescription":
			assertItemParams(t, params, "Revolution Case")
			_, _ = io.WriteString(w, `{"data":{"appid":730,"classid":"5189384637","market_hash_name":"Revolution Case","market_bucket_group_id":"G1890263004","commodity":true}}`)
		case "QueryListingsForItem":
			if len(params) != 1 {
				t.Fatalf("listing params=%s", r.URL.Query().Get("qp"))
			}
			var request map[string]any
			if err := json.Unmarshal(params[0], &request); err != nil {
				t.Fatal(err)
			}
			if request["appid"] != float64(730) || request["strItemName"] != "G1807209A023004" || request["start"] != float64(20) {
				t.Fatalf("listing request=%v", request)
			}
			for _, key := range []string{"filters", "accessoryFilters", "propertyFilters"} {
				if _, ok := request[key]; !ok {
					t.Fatalf("listing request missing %s", key)
				}
			}
			_, _ = io.WriteString(w, `{"data":{"more":false,"start":20,"total_count":21,"listings":[{"listingid":"1","unPrice":100,"unFee":15,"eCurrency":23,"description":{"market_hash_name":"AK-47 | Redline (Field-Tested)"},"asset":{"assetid":"2","asset_properties":[{"propertyid":2,"float_value":0.25}],"asset_accessories":[{"classid":"4614639265","standalone_properties":[],"parent_relationship_properties":[{"propertyid":4,"float_value":1}],"nested_accessories":[],"description":{"market_hash_name":"Sticker | FaZe Clan | Stockholm 2021"}}]}}],"facets":[]}}`)
		default:
			t.Fatalf("action=%q", action)
		}
	}))
	t.Cleanup(server.Close)
	client := mustClient(t, server)

	history, err := client.PriceHistory(context.Background(), 730, "Revolution Case")
	if err != nil || history.Currency != 23 || len(history.Prices) != 1 || history.Prices[0].Purchases != 3 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	description, err := client.ItemDescription(context.Background(), 730, "Revolution Case")
	if err != nil || description.MarketBucketGroupID != "G1890263004" || !description.Commodity {
		t.Fatalf("description=%+v err=%v", description, err)
	}
	listings, err := client.Listings(context.Background(), ListingsRequest{AppID: 730, GroupID: "G1807209A023004", Start: 20})
	if err != nil || listings.TotalCount != 21 || len(listings.Listings) != 1 {
		t.Fatalf("listings=%+v err=%v", listings, err)
	}
	properties := listings.Listings[0].Asset.Properties
	if len(properties) != 1 || properties[0].FloatValue == nil || *properties[0].FloatValue != 0.25 {
		t.Fatalf("properties=%+v", properties)
	}
	accessories := listings.Listings[0].Asset.Accessories
	if len(accessories) != 1 || accessories[0].ClassID != "4614639265" ||
		accessories[0].Description.MarketHashName != "Sticker | FaZe Clan | Stockholm 2021" ||
		len(accessories[0].ParentRelationshipProperties) != 1 {
		t.Fatalf("accessories=%+v", accessories)
	}
}

func TestClientOrderBookUsesLoadAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != orderbookPath || r.URL.Query().Get("q") != "Load" {
			t.Fatalf("url=%s", r.URL.String())
		}
		if r.Header.Get(queryActionHeader) != queryActionHeaderVal {
			t.Fatalf("%s=%q", queryActionHeader, r.Header.Get(queryActionHeader))
		}
		var params []json.RawMessage
		if err := json.Unmarshal([]byte(r.URL.Query().Get("qp")), &params); err != nil {
			t.Fatal(err)
		}
		assertItemParams(t, params, "Revolution Case")
		_, _ = io.WriteString(w, `{"data":{"success":true,"data":{"amtMaxBuyOrder":21,"amtMinSellOrder":22,"eCurrency":23,"cBuyOrders":3,"cSellOrders":4,"rgCompactBuyOrders":[21,3],"rgCompactSellOrders":[22,4]}}}`)
	}))
	t.Cleanup(server.Close)
	result, err := mustClient(t, server).OrderBook(context.Background(), 730, "Revolution Case")
	if err != nil || result.Data == nil || result.Data.MaxBuyOrder == nil || *result.Data.MaxBuyOrder != 21 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func assertItemParams(t *testing.T, params []json.RawMessage, marketHashName string) {
	t.Helper()
	if len(params) != 2 {
		t.Fatalf("params=%s", params)
	}
	var appID int64
	var name string
	if err := json.Unmarshal(params[0], &appID); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(params[1], &name); err != nil {
		t.Fatal(err)
	}
	if appID != 730 || name != marketHashName {
		t.Fatalf("appid=%d name=%q", appID, name)
	}
}

func mustClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(ClientOptions{BaseURL: server.URL, HTTPClient: server.Client(), Cookie: "steamLoginSecure=test"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
