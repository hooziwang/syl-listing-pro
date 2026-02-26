package app

import (
	"context"

	"syl-listing-pro/internal/config"
)

func RunSetKey(_ context.Context, key string) error {
	return config.SaveSYLListingKey(key)
}
