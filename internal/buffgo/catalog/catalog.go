package catalog

import (
	"context"
	"fmt"
)

// Service coordinates catalog import and Steam pull into PostgreSQL.
type Service struct {
	Store  *Store
	Puller *Puller
}

// NewService constructs a Service. puller may be nil until Pull is used.
func NewService(store *Store, puller *Puller) *Service {
	return &Service{Store: store, Puller: puller}
}

// ImportResult is the outcome of writing catalog items for an appid.
type ImportResult struct {
	AppID  int64
	Upsert UpsertResult
	Count  int64 // total items for appid after write
	Source string
	Sample []Item // up to a few rows for logging
}

// ImportItems ensures the game row exists, upserts items, and reports counts.
func (s *Service) ImportItems(ctx context.Context, appid int64, items []Item, source string) (ImportResult, error) {
	var res ImportResult
	res.AppID = appid
	res.Source = source
	if s == nil || s.Store == nil {
		return res, fmt.Errorf("nil catalog service/store")
	}
	if appid <= 0 {
		return res, fmt.Errorf("appid is required")
	}
	if len(items) == 0 {
		return res, fmt.Errorf("no items to import")
	}

	// Fill missing appids, but never rewrite a product from another game.
	norm := make([]Item, 0, len(items))
	for i, it := range items {
		if it.AppID == 0 {
			it.AppID = appid
		} else if it.AppID != appid {
			return res, fmt.Errorf("item[%d]: appid %d does not match target appid %d", i, it.AppID, appid)
		}
		if err := it.Normalize(); err != nil {
			return res, fmt.Errorf("item[%d]: %w", i, err)
		}
		norm = append(norm, it)
	}

	g := KnownGame(appid)
	if err := s.Store.EnsureGame(ctx, g); err != nil {
		return res, err
	}
	up, err := s.Store.UpsertItems(ctx, norm)
	if err != nil {
		return res, err
	}
	res.Upsert = up
	n, err := s.Store.CountByAppID(ctx, appid)
	if err != nil {
		return res, err
	}
	res.Count = n
	sample, err := s.Store.ListByAppID(ctx, appid, 5)
	if err != nil {
		return res, err
	}
	res.Sample = sample
	return res, nil
}

// ImportFile loads JSON and imports into PG for the resolved appid.
func (s *Service) ImportFile(ctx context.Context, path string, defaultAppID int64) (ImportResult, error) {
	items, appid, err := LoadImportFile(path, defaultAppID)
	if err != nil {
		return ImportResult{}, err
	}
	return s.ImportItems(ctx, appid, items, "file:"+path)
}

// PullAndImport performs a minimal Steam market pull and upserts catalog rows.
func (s *Service) PullAndImport(ctx context.Context, opts PullOptions) (ImportResult, PullResult, error) {
	var empty ImportResult
	if s == nil || s.Store == nil {
		return empty, PullResult{}, fmt.Errorf("nil catalog service/store")
	}
	puller := s.Puller
	if puller == nil {
		puller = NewPuller(opts)
	}
	pulled, err := puller.Pull(ctx, opts)
	if err != nil {
		return empty, pulled, err
	}
	if len(pulled.Items) == 0 {
		return empty, pulled, fmt.Errorf("steam pull returned 0 items for appid=%d", opts.AppID)
	}
	imp, err := s.ImportItems(ctx, opts.AppID, pulled.Items, "steam:search/render")
	return imp, pulled, err
}
