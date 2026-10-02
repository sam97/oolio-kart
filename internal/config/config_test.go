package config

import (
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/internal/logging"
)

// loadFrom loads the config with only vars set among the known variables.
// Viper treats empty variables as unset, so blanking the rest isolates the
// test from the machine's environment.
func loadFrom(t *testing.T, vars map[string]string) (Config, error) {
	t.Helper()
	for key := range defaults {
		t.Setenv(strings.ToUpper(key), "")
	}
	t.Setenv("CONFIG_FILE", "")
	for name, value := range vars {
		t.Setenv(name, value)
	}
	return Load()
}

func TestDefaults(t *testing.T) {
	cfg, err := loadFrom(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" || cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != logging.JSON {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if want := (APIKeys{"apitest": {"create_order"}}); !reflect.DeepEqual(cfg.APIKeys, want) {
		t.Errorf("APIKeys = %v, want %v", cfg.APIKeys, want)
	}
	if cfg.CouponRateLimit != 20 || cfg.OrderRateLimit != 10 || cfg.CouponRateWindow != time.Minute {
		t.Errorf("unexpected rate limits: %+v", cfg)
	}
	if cfg.ReadHeaderTimeout != 5*time.Second || cfg.CacheMaxEntries != 10000 {
		t.Errorf("unexpected server defaults: %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 0 || !reflect.DeepEqual(cfg.CORSAllowedOrigins, []string{"*"}) {
		t.Errorf("unexpected network defaults: %+v", cfg)
	}
}

func TestOverrides(t *testing.T) {
	cfg, err := loadFrom(t, map[string]string{
		"API_KEYS":             "apitest:create_order|read, readonly:",
		"LOG_LEVEL":            "debug",
		"LOG_FORMAT":           "text",
		"TRUSTED_PROXIES":      "10.0.0.0/8,::1/128",
		"ORDER_RATE_LIMIT":     "3",
		"CORS_ALLOWED_ORIGINS": "https://a.example,https://b.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := APIKeys{"apitest": {"create_order", "read"}, "readonly": {}}
	if !reflect.DeepEqual(cfg.APIKeys, wantKeys) {
		t.Errorf("APIKeys = %v, want %v", cfg.APIKeys, wantKeys)
	}
	wantProxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	if !reflect.DeepEqual(cfg.TrustedProxies, wantProxies) {
		t.Errorf("TrustedProxies = %v, want %v", cfg.TrustedProxies, wantProxies)
	}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, []string{"https://a.example", "https://b.example"}) {
		t.Errorf("CORSAllowedOrigins = %v", cfg.CORSAllowedOrigins)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != logging.Text || cfg.OrderRateLimit != 3 {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kart.yaml")
	file := `
addr: ":9090"
order_rate_limit: 4
trusted_proxies: ["10.0.0.0/8"]
api_keys: "filekey:create_order"
`
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}

	// The environment wins over the file.
	cfg, err := loadFrom(t, map[string]string{"CONFIG_FILE": path, "ORDER_RATE_LIMIT": "7"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9090" || cfg.OrderRateLimit != 7 {
		t.Errorf("Addr = %q, OrderRateLimit = %d; want :9090, 7", cfg.Addr, cfg.OrderRateLimit)
	}
	if want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}; !reflect.DeepEqual(cfg.TrustedProxies, want) {
		t.Errorf("TrustedProxies = %v", cfg.TrustedProxies)
	}
	if want := (APIKeys{"filekey": {"create_order"}}); !reflect.DeepEqual(cfg.APIKeys, want) {
		t.Errorf("APIKeys = %v", cfg.APIKeys)
	}

	if _, err := loadFrom(t, map[string]string{"CONFIG_FILE": filepath.Join(t.TempDir(), "missing.yaml")}); err == nil {
		t.Error("missing config file was accepted")
	}
}

func TestInvalid(t *testing.T) {
	tests := map[string]map[string]string{
		"no api keys":      {"API_KEYS": " , "},
		"empty key":        {"API_KEYS": ":create_order"},
		"duplicate key":    {"API_KEYS": "a:x,a:y"},
		"bad log format":   {"LOG_FORMAT": "xml"},
		"bad log level":    {"LOG_LEVEL": "loud"},
		"bad proxy":        {"TRUSTED_PROXIES": "10.0.0.1"},
		"zero rate limit":  {"COUPON_RATE_LIMIT": "0"},
		"bad rate limit":   {"COUPON_RATE_LIMIT": "lots"},
		"zero window":      {"ORDER_RATE_WINDOW": "0s"},
		"negative ttl":     {"COUPON_CACHE_TTL": "-1m"},
		"no cache entries": {"CACHE_MAX_ENTRIES": "0"},
		"bad duration":     {"READ_TIMEOUT": "soon"},
		"negative timeout": {"SHUTDOWN_TIMEOUT": "-1s"},
	}
	for name, vars := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadFrom(t, vars); err == nil {
				t.Errorf("load(%v) succeeded", vars)
			}
		})
	}
}

func TestDuplicateKeyErrorHidesKey(t *testing.T) {
	_, err := loadFrom(t, map[string]string{"API_KEYS": "secret123:x,secret123:y"})
	if err == nil || strings.Contains(err.Error(), "secret123") {
		t.Errorf("err = %v, want an error that does not echo the key", err)
	}
}
