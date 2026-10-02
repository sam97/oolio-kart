// Package config loads the API server configuration from environment
// variables and, optionally, a config file named by CONFIG_FILE.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"reflect"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/sam97/oolio-kart/api/internal/logging"
	"github.com/spf13/viper"
)

type Config struct {
	Addr              string        `mapstructure:"addr"`
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
	ShutdownTimeout   time.Duration `mapstructure:"shutdown_timeout"`

	LogLevel  slog.Level     `mapstructure:"log_level"`
	LogFormat logging.Format `mapstructure:"log_format"`

	// APIKeys maps each accepted api_key to its scopes.
	APIKeys APIKeys `mapstructure:"api_keys"`

	CouponsURL     string        `mapstructure:"coupons_url"`
	CouponsTimeout time.Duration `mapstructure:"coupons_timeout"`

	// A cache TTL of 0 disables caching of that kind of answer.
	CouponCacheTTL         time.Duration `mapstructure:"coupon_cache_ttl"`
	CouponNegativeCacheTTL time.Duration `mapstructure:"coupon_negative_cache_ttl"`
	CacheMaxEntries        int           `mapstructure:"cache_max_entries"`

	CouponRateLimit  int           `mapstructure:"coupon_rate_limit"`
	CouponRateWindow time.Duration `mapstructure:"coupon_rate_window"`
	OrderRateLimit   int           `mapstructure:"order_rate_limit"`
	OrderRateWindow  time.Duration `mapstructure:"order_rate_window"`

	// TrustedProxies lists the CIDRs of reverse proxies in front of the
	// server. X-Forwarded-For is only honoured on connections from them, so
	// clients cannot spoof their IP to dodge rate limits.
	TrustedProxies []netip.Prefix `mapstructure:"trusted_proxies"`

	CORSAllowedOrigins []string `mapstructure:"cors_allowed_origins"`
}

// defaults lists every key with its default. Viper only reads environment
// variables for keys it knows about, so each key must appear here. Each key
// is read from the environment variable of the same name in upper case.
var defaults = map[string]string{
	"addr":                      ":8080",
	"read_header_timeout":       "5s",
	"read_timeout":              "10s",
	"write_timeout":             "15s",
	"idle_timeout":              "60s",
	"shutdown_timeout":          "15s",
	"log_level":                 "info",
	"log_format":                "json",
	"api_keys":                  "apitest:create_order", // TODO: These keys should be in secrets or other data store
	"coupons_url":               "http://localhost:8081",
	"coupons_timeout":           "2s",
	"coupon_cache_ttl":          "5m",
	"coupon_negative_cache_ttl": "1m",
	"cache_max_entries":         "10000",
	"coupon_rate_limit":         "20",
	"coupon_rate_window":        "1m",
	"order_rate_limit":          "10",
	"order_rate_window":         "1m",
	"trusted_proxies":           "",
	"cors_allowed_origins":      "*",
}

// Load reads the configuration. Environment variables take precedence over
// the file named by CONFIG_FILE (yaml, json or toml), which takes precedence
// over the defaults.
func Load() (Config, error) {
	reader := viper.New()
	for key, value := range defaults {
		reader.SetDefault(key, value)
	}
	reader.AutomaticEnv()

	if path := reader.GetString("config_file"); path != "" {
		reader.SetConfigFile(path)
		if err := reader.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
	}

	var cfg Config
	err := reader.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.TextUnmarshallerHookFunc(),
		mapstructure.StringToTimeDurationHookFunc(),
		splitList,
	)))
	if err != nil {
		return Config{}, err
	}
	return cfg, cfg.validate()
}

// splitList decodes a comma-separated string into a slice of any element
// type, dropping blank entries. Each element is then decoded on its own, so
// slices of TextUnmarshalers such as netip.Prefix work.
func splitList(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String || to.Kind() != reflect.Slice {
		return data, nil
	}
	items := []string{}
	for item := range strings.SplitSeq(data.(string), ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items, nil
}

func (c Config) validate() error {
	var problems []error
	positive := map[string]time.Duration{
		"READ_HEADER_TIMEOUT": c.ReadHeaderTimeout,
		"READ_TIMEOUT":        c.ReadTimeout,
		"WRITE_TIMEOUT":       c.WriteTimeout,
		"IDLE_TIMEOUT":        c.IdleTimeout,
		"SHUTDOWN_TIMEOUT":    c.ShutdownTimeout,
		"COUPONS_TIMEOUT":     c.CouponsTimeout,
		"COUPON_RATE_WINDOW":  c.CouponRateWindow,
		"ORDER_RATE_WINDOW":   c.OrderRateWindow,
	}
	for name, value := range positive {
		if value <= 0 {
			problems = append(problems, fmt.Errorf("%s must be positive", name))
		}
	}
	if c.CouponCacheTTL < 0 || c.CouponNegativeCacheTTL < 0 {
		problems = append(problems, errors.New("coupon cache TTLs must not be negative"))
	}
	if c.CacheMaxEntries < 1 {
		problems = append(problems, errors.New("CACHE_MAX_ENTRIES must be at least 1"))
	}
	if c.CouponRateLimit < 1 || c.OrderRateLimit < 1 {
		problems = append(problems, errors.New("rate limits must be at least 1"))
	}
	if len(c.APIKeys) == 0 {
		problems = append(problems, errors.New("API_KEYS must contain at least one key"))
	}
	return errors.Join(problems...)
}

// APIKeys maps api keys to their scopes. It is written as comma-separated
// key:scopes entries, with scopes separated by "|":
//
//	apitest:create_order,readonly:
type APIKeys map[string][]string

func (k *APIKeys) UnmarshalText(text []byte) error {
	keys := APIKeys{}
	for entry := range strings.SplitSeq(string(text), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, scopes, _ := strings.Cut(entry, ":")
		if key == "" {
			return fmt.Errorf("API key entry %q has an empty key", entry)
		}
		if _, dup := keys[key]; dup {
			return errors.New("API_KEYS lists a key twice")
		}
		keys[key] = []string{}
		for scope := range strings.SplitSeq(scopes, "|") {
			if scope = strings.TrimSpace(scope); scope != "" {
				keys[key] = append(keys[key], scope)
			}
		}
	}
	*k = keys
	return nil
}
