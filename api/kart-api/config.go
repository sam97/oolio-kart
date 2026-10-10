package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/sam97/oolio-kart/pkg/helpers/config"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
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

// loadConfig reads the configuration from the env files in dir and the
// environment; see package config for the layering.
func loadConfig(dir string) (Config, error) {
	var cfg Config
	if err := config.Load(&cfg, dir); err != nil {
		return Config{}, err
	}
	return cfg, cfg.validate()
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
