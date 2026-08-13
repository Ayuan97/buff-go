package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"buff-go/internal/catalog"
)

// ErrMappingConflict means a platform identity already targets another product.
var ErrMappingConflict = errors.New("platform mapping targets another product")

// CreateSteamProduct inserts a Steam catalog row keyed by (appid, market_hash_name).
func (s *Store) CreateSteamProduct(ctx context.Context, appid int64, name string) (catalog.SteamProduct, error) {
	var product catalog.SteamProduct
	if err := s.validate(); err != nil {
		return product, err
	}
	if err := validateSteamProductInput(appid, name); err != nil {
		return product, err
	}

	var productID int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO steam_products (appid, name)
VALUES ($1, $2)
ON CONFLICT (appid, name) DO UPDATE SET name = EXCLUDED.name
RETURNING product_id`, appid, name).Scan(&productID)
	if err != nil {
		return product, fmt.Errorf("create steam product: %w", err)
	}
	product = catalog.SteamProduct{
		ProductID: catalog.ProductID(productID),
		AppID:     appid,
		Name:      name,
	}
	if err := validateSteamProduct(product); err != nil {
		return catalog.SteamProduct{}, fmt.Errorf("created steam product: %w", err)
	}
	return product, nil
}

// SteamProduct returns one product by its stable internal ProductID.
func (s *Store) SteamProduct(ctx context.Context, productID catalog.ProductID) (catalog.SteamProduct, bool, error) {
	var product catalog.SteamProduct
	if err := s.validate(); err != nil {
		return product, false, err
	}
	if err := validateProductID(productID); err != nil {
		return product, false, err
	}

	var id int64
	err := s.db.QueryRowContext(ctx, `
SELECT product_id, appid, name
FROM steam_products
WHERE product_id = $1`, int64(productID)).Scan(&id, &product.AppID, &product.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.SteamProduct{}, false, nil
	}
	if err != nil {
		return catalog.SteamProduct{}, false, fmt.Errorf("read steam product: %w", err)
	}
	product.ProductID = catalog.ProductID(id)
	if err := validateSteamProduct(product); err != nil {
		return catalog.SteamProduct{}, false, fmt.Errorf("stored steam product: %w", err)
	}
	return product, true, nil
}

// ListSteamProductsByAppID returns products in stable ProductID order.
func (s *Store) ListSteamProductsByAppID(ctx context.Context, appid int64) ([]catalog.SteamProduct, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := validateAppID(appid); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT product_id, appid, name
FROM steam_products
WHERE appid = $1
ORDER BY product_id`, appid)
	if err != nil {
		return nil, fmt.Errorf("list steam products: %w", err)
	}
	defer rows.Close()

	products := make([]catalog.SteamProduct, 0)
	for rows.Next() {
		var product catalog.SteamProduct
		var productID int64
		if err := rows.Scan(&productID, &product.AppID, &product.Name); err != nil {
			return nil, fmt.Errorf("scan steam product: %w", err)
		}
		product.ProductID = catalog.ProductID(productID)
		if err := validateSteamProduct(product); err != nil {
			return nil, fmt.Errorf("stored steam product: %w", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list steam products: %w", err)
	}
	return products, nil
}

// ListSteamProductsAfter returns products with product_id greater than after,
// in stable identity order, limited to limit rows.
func (s *Store) ListSteamProductsAfter(ctx context.Context, appid int64, after catalog.ProductID, limit int) ([]catalog.SteamProduct, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := validateAppID(appid); err != nil {
		return nil, err
	}
	if after < 0 {
		return nil, fmt.Errorf("after product_id must be non-negative")
	}
	if limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT product_id, appid, name
FROM steam_products
WHERE appid = $1 AND product_id > $2
ORDER BY product_id
LIMIT $3`, appid, int64(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list steam products after: %w", err)
	}
	defer rows.Close()
	products := make([]catalog.SteamProduct, 0)
	for rows.Next() {
		var product catalog.SteamProduct
		var productID int64
		if err := rows.Scan(&productID, &product.AppID, &product.Name); err != nil {
			return nil, fmt.Errorf("scan steam product: %w", err)
		}
		product.ProductID = catalog.ProductID(productID)
		if err := validateSteamProduct(product); err != nil {
			return nil, fmt.Errorf("stored steam product: %w", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list steam products after: %w", err)
	}
	return products, nil
}

// PutPlatformMapping stores a verified platform mapping without ever rebinding it.
// Repeating the same target is idempotent; another target returns ErrMappingConflict.
func (s *Store) PutPlatformMapping(ctx context.Context, mapping catalog.PlatformMapping) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := validatePlatformMapping(mapping); err != nil {
		return err
	}

	var insertedProductID int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO platform_product_mappings (platform, appid, platform_item_id, product_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (platform, appid, platform_item_id) DO NOTHING
RETURNING product_id`,
		mapping.Platform, mapping.AppID, mapping.PlatformItemID, int64(mapping.ProductID),
	).Scan(&insertedProductID)
	if err == nil {
		if catalog.ProductID(insertedProductID) != mapping.ProductID {
			return fmt.Errorf("inserted platform mapping target mismatch")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("put platform mapping: %w", err)
	}

	existing, found, err := s.PlatformMapping(ctx, mapping.Platform, mapping.AppID, mapping.PlatformItemID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("platform mapping conflict row not found")
	}
	return compareMappingTarget(mapping.ProductID, existing.ProductID)
}

// PlatformMapping returns one exact platform identity mapping.
func (s *Store) PlatformMapping(ctx context.Context, platform string, appid int64, platformItemID string) (catalog.PlatformMapping, bool, error) {
	var mapping catalog.PlatformMapping
	if err := s.validate(); err != nil {
		return mapping, false, err
	}
	if err := validatePlatformMappingKey(platform, appid, platformItemID); err != nil {
		return mapping, false, err
	}

	var productID int64
	err := s.db.QueryRowContext(ctx, `
SELECT platform, appid, platform_item_id, product_id
FROM platform_product_mappings
WHERE platform = $1 AND appid = $2 AND platform_item_id = $3`,
		platform, appid, platformItemID,
	).Scan(&mapping.Platform, &mapping.AppID, &mapping.PlatformItemID, &productID)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.PlatformMapping{}, false, nil
	}
	if err != nil {
		return catalog.PlatformMapping{}, false, fmt.Errorf("read platform mapping: %w", err)
	}
	mapping.ProductID = catalog.ProductID(productID)
	if err := validatePlatformMapping(mapping); err != nil {
		return catalog.PlatformMapping{}, false, fmt.Errorf("stored platform mapping: %w", err)
	}
	return mapping, true, nil
}

func validateSteamProductInput(appid int64, name string) error {
	if err := validateAppID(appid); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("steam product name is required")
	}
	return nil
}

func validateSteamProduct(product catalog.SteamProduct) error {
	if err := validateProductID(product.ProductID); err != nil {
		return err
	}
	return validateSteamProductInput(product.AppID, product.Name)
}

func validateProductID(productID catalog.ProductID) error {
	if productID <= 0 {
		return fmt.Errorf("product_id must be positive")
	}
	return nil
}

func validatePlatformMappingKey(platform string, appid int64, platformItemID string) error {
	if err := validatePlatform(platform); err != nil {
		return err
	}
	if err := validateAppID(appid); err != nil {
		return err
	}
	if platformItemID == "" {
		return fmt.Errorf("platform_item_id is required")
	}
	return nil
}

func validatePlatformMapping(mapping catalog.PlatformMapping) error {
	if err := validatePlatformMappingKey(mapping.Platform, mapping.AppID, mapping.PlatformItemID); err != nil {
		return err
	}
	return validateProductID(mapping.ProductID)
}

func compareMappingTarget(requested, existing catalog.ProductID) error {
	if requested != existing {
		return ErrMappingConflict
	}
	return nil
}
