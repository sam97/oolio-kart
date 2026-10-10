package scanner

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
)

// Bucket files are kept between scans, laid out so a later incremental build
// can reuse them. Stats.Layout describes them once a scan succeeds:
//
//	<dir>/
//	  couponbase1.gz-3f9a1c02/         <source name>-<fnv32a of the name>
//	    b000.dat b001.dat … b189.dat   that source's codes in each bucket: packed
//	                                   uint64s, sorted and de-duplicated once counted
//	  couponbase2.gz-77b0e4d1/
//	  …
func (b *Buckets) layout(sources []couponsource.Source, files [][]*bucketFile, plan plan) couponstore.Layout {
	layout := couponstore.Layout{Hash: b.hashName, Buckets: plan.buckets, ByteOrder: nativeByteOrder()}
	for i, source := range sources {
		var codes int64
		for _, file := range files[i] {
			codes += file.codes
		}
		name := source.Info().Name
		layout.Sources = append(layout.Sources, couponstore.SourceLayout{Name: name, Dir: sourceDir(name), Codes: codes})
	}
	return layout
}

// sourceDir names a source's bucket folder. The hash keeps names that clean
// up to the same string apart.
func sourceDir(name string) string {
	hash := fnv.New32a()
	hash.Write([]byte(name))
	clean := strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || ('0' <= r && r <= '9') || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') {
			return r
		}
		return '_'
	}, name)
	return fmt.Sprintf("%s-%08x", clean, hash.Sum32())
}

func bucketName(bucket int) string {
	return fmt.Sprintf("b%03d.dat", bucket)
}

var sourceDirPattern = regexp.MustCompile(`^.+-[0-9a-f]{8}$`)

// wipe removes the previous scan's source folders from dir. Anything else in
// dir is left alone.
func wipe(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && sourceDirPattern.MatchString(entry.Name()) {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// asBytes views codes as raw bytes for file I/O, without copying. The layout
// records the byte order they were written in.
func asBytes(values []uint64) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(values))), len(values)*8)
}

func nativeByteOrder() string {
	probe := []uint64{1}
	if binary.LittleEndian.Uint64(asBytes(probe)) == 1 {
		return "little"
	}
	return "big"
}

// writeValues replaces the file at path with values, through a temporary
// file so a crash never leaves it half-written.
func writeValues(path string, values []uint64) error {
	temp := path + ".tmp"
	if err := os.WriteFile(temp, asBytes(values), 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
