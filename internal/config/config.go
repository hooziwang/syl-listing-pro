package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"syl-listing-pro/internal/util"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	Auth   AuthConfig   `yaml:"auth"`
	Rules  RulesConfig  `yaml:"rules"`
	Run    RunConfig    `yaml:"run"`
}

type ServerConfig struct {
	BaseURL string `yaml:"base_url"`
}

type AuthConfig struct {
	SYLListingKey string `yaml:"syl_listing_key"`
}

type RulesConfig struct {
	CacheDir      string `yaml:"cache_dir"`
	PublicKeyPath string `yaml:"public_key_path"`
}

type RunConfig struct {
	PollIntervalMs    int `yaml:"poll_interval_ms"`
	PollTimeoutSecond int `yaml:"poll_timeout_second"`
}

func Default() (Config, error) {
	cacheDir, err := util.DefaultCacheDir()
	if err != nil {
		return Config{}, err
	}
	return Config{
		Server: ServerConfig{BaseURL: "http://127.0.0.1:8080"},
		Auth:   AuthConfig{SYLListingKey: ""},
		Rules: RulesConfig{
			CacheDir:      cacheDir,
			PublicKeyPath: "",
		},
		Run: RunConfig{PollIntervalMs: 800, PollTimeoutSecond: 900},
	}, nil
}

func ResolvePath(input string) (string, error) {
	if input != "" {
		return input, nil
	}
	return util.DefaultConfigPath()
}

func LoadOrInit(path string) (Config, error) {
	def, err := Default()
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("读取配置失败: %w", err)
		}
		if err := Save(path, def); err != nil {
			return Config{}, err
		}
		return def, nil
	}
	cfg := def
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置失败: %w", err)
	}
	if cfg.Server.BaseURL == "" {
		cfg.Server.BaseURL = def.Server.BaseURL
	}
	if cfg.Rules.CacheDir == "" {
		cfg.Rules.CacheDir = def.Rules.CacheDir
	}
	if cfg.Run.PollIntervalMs <= 0 {
		cfg.Run.PollIntervalMs = def.Run.PollIntervalMs
	}
	if cfg.Run.PollTimeoutSecond <= 0 {
		cfg.Run.PollTimeoutSecond = def.Run.PollTimeoutSecond
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("写配置失败: %w", err)
	}
	return nil
}
