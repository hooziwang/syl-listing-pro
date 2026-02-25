package app

import (
	"context"

	"syl-listing-pro/internal/config"
)

func RunSetKey(_ context.Context, path, key string) error {
	cfgPath, err := config.ResolvePath(path)
	if err != nil {
		return err
	}
	cfg, err := config.LoadOrInit(cfgPath)
	if err != nil {
		return err
	}
	cfg.Auth.SYLListingKey = key
	return config.Save(cfgPath, cfg)
}
