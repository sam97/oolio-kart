package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

type testConfig struct {
	Name    string         `mapstructure:"name"`
	Port    int            `mapstructure:"port"`
	Timeout time.Duration  `mapstructure:"timeout"`
	Origins []string       `mapstructure:"origins"`
	Proxies []netip.Prefix `mapstructure:"proxies"`
}

const testDefaults = `
NAME=defaults
PORT=1
TIMEOUT=1s
ORIGINS=*
PROXIES=
`

// setup writes files into a fresh directory and clears the variables the
// loader reads.
func setup(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range append(Keys(testConfig{}), FileVar) {
		t.Setenv(key, "")
	}
	return dir
}

func TestDefaultsOnly(t *testing.T) {
	dir := setup(t, map[string]string{DefaultsFile: testDefaults})

	var cfg testConfig
	if err := Load(&cfg, dir); err != nil {
		t.Fatal(err)
	}
	want := testConfig{Name: "defaults", Port: 1, Timeout: time.Second, Origins: []string{"*"}, Proxies: []netip.Prefix{}}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("cfg = %+v, want %+v", cfg, want)
	}
}

func TestLayers(t *testing.T) {
	dir := setup(t, map[string]string{
		DefaultsFile: testDefaults,
		LocalFile:    "NAME=local\nPORT=2\nTIMEOUT=2s\n",
		"prod.env":   "PORT=3\nTIMEOUT=3s\nPROXIES=10.0.0.0/8, ::1/128\n",
	})
	t.Setenv(FileVar, filepath.Join(dir, "prod.env"))
	t.Setenv("TIMEOUT", "4s")

	var cfg testConfig
	if err := Load(&cfg, dir); err != nil {
		t.Fatal(err)
	}
	// Each layer wins over the ones before it, and keys a layer leaves out
	// fall through.
	if cfg.Origins[0] != "*" || cfg.Name != "local" || cfg.Port != 3 || cfg.Timeout != 4*time.Second {
		t.Errorf("cfg = %+v, want origins from defaults, name from .env, port from ENV_FILE, timeout from env", cfg)
	}
	wantProxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	if !slices.Equal(cfg.Proxies, wantProxies) {
		t.Errorf("Proxies = %v, want %v", cfg.Proxies, wantProxies)
	}
}

func TestMissingFiles(t *testing.T) {
	var cfg testConfig
	if err := Load(&cfg, setup(t, nil)); err == nil {
		t.Error("missing defaults file was accepted")
	}

	dir := setup(t, map[string]string{DefaultsFile: testDefaults})
	t.Setenv(FileVar, filepath.Join(dir, "missing.env"))
	if err := Load(&cfg, dir); err == nil {
		t.Error("missing ENV_FILE was accepted")
	}
}

func TestUnsetField(t *testing.T) {
	dir := setup(t, map[string]string{DefaultsFile: "NAME=only\n"})

	var cfg testConfig
	if err := Load(&cfg, dir); err == nil {
		t.Error("fields without a default were accepted")
	}
}

func TestByteSize(t *testing.T) {
	valid := map[string]ByteSize{
		"256MiB":  256 << 20,
		"256 MiB": 256 << 20,
		"256MB":   256_000_000,
		"1.5GiB":  3 << 29,
		"1024":    1024,
	}
	for text, want := range valid {
		var size ByteSize
		if err := size.UnmarshalText([]byte(text)); err != nil || size != want {
			t.Errorf("ByteSize(%q) = %d, %v; want %d", text, size, err, want)
		}
	}
	for _, text := range []string{"", "lots", "-5MiB", "99999EiB"} {
		var size ByteSize
		if err := size.UnmarshalText([]byte(text)); err == nil {
			t.Errorf("ByteSize(%q) = %d, want an error", text, size)
		}
	}
}
