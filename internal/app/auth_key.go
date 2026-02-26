package app

import (
	"errors"
	"fmt"

	"syl-listing-pro/internal/config"
)

func loadSYLKeyForRun() (string, error) {
	key, err := config.LoadSYLListingKey()
	if err != nil {
		if errors.Is(err, config.ErrSYLKeyNotConfigured) {
			return "", fmt.Errorf("尚未配置 KEY，需要执行\nsyl-listing-pro set key <SYL_LISTING_KEY>")
		}
		return "", err
	}
	return key, nil
}
