// Package config loads service configuration from layered env files and the
// process environment.
//
// Each layer overrides the ones before it:
//
//  1. .env.defaults: every key with its default. Committed and required.
//  2. .env: local development overrides. Gitignored and optional.
//  3. The file named by ENV_FILE, e.g. a staging or production file. Required
//     when ENV_FILE is set.
//  4. Environment variables of the same name.
//
// The override layers may be partial. Viper treats empty environment
// variables as unset.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

const (
	DefaultsFile = ".env.defaults"
	LocalFile    = ".env"

	// FileVar names the environment variable that points at an extra
	// override file.
	FileVar = "ENV_FILE"
)

// Load fills target, a pointer to a struct with mapstructure tags, from the
// layers described in the package doc. dir holds .env.defaults and .env.
// Every field of target must have a key in .env.defaults.
func Load(target any, dir string) error {
	reader := viper.New()
	reader.SetConfigType("env")

	defaults, err := os.Open(filepath.Join(dir, DefaultsFile))
	if err != nil {
		return fmt.Errorf("%w (run from the repository root, or set the working directory to where %s is)", err, DefaultsFile)
	}
	defer defaults.Close()
	if err := reader.ReadConfig(defaults); err != nil {
		return fmt.Errorf("read %s: %w", DefaultsFile, err)
	}

	if err := merge(reader, filepath.Join(dir, LocalFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if path := os.Getenv(FileVar); path != "" {
		if err := merge(reader, path); err != nil {
			return err
		}
	}
	reader.AutomaticEnv()

	return reader.Unmarshal(target,
		viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(),
			mapstructure.StringToTimeDurationHookFunc(),
			splitList,
		)),
		func(decoder *mapstructure.DecoderConfig) { decoder.ErrorUnset = true },
	)
}

func merge(reader *viper.Viper, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := reader.MergeConfig(file); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
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

// ByteSize is a number of bytes written in a human form such as "256MiB" or
// "1.5 GB". IEC units (KiB, MiB, GiB) are powers of 1024; SI units (KB, MB,
// GB) are powers of 1000.
type ByteSize int64

func (size *ByteSize) UnmarshalText(text []byte) error {
	bytes, err := humanize.ParseBytes(string(text))
	if err != nil {
		return err
	}
	if bytes > math.MaxInt64 {
		return fmt.Errorf("%s is too large", text)
	}
	*size = ByteSize(bytes)
	return nil
}

// Keys returns the environment variable names that fill target's fields, so
// tests can clear them.
func Keys(target any) []string {
	var keys []string
	kind := reflect.TypeOf(target)
	if kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	for field := range kind.Fields() {
		if tag := field.Tag.Get("mapstructure"); tag != "" {
			keys = append(keys, strings.ToUpper(tag))
		}
	}
	return keys
}
