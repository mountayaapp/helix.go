package bucket

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/mountayaapp/helix.go/errorstack"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"
)

// DriverLocal uses a directory of the local filesystem. Config.Bucket is the
// directory, which must exist; a relative one is resolved against the working
// directory when Connect is called.
//
// None of Config.Region, Config.Endpoint, Config.AccessKeyID or
// Config.SecretAccessKey applies.
//
// Config example:
//
//	bucket.Config{
//	  Driver: bucket.DriverLocal,
//	  Bucket: "/path/to/folder",
//	}
const DriverLocal Driver = "local"

// driverLocal is the behaviour behind DriverLocal.
type driverLocal struct{}

// validate ensures that no field the driver has no use for is set. Any path is a
// valid directory name; whether it exists is for Connect to find out.
func (driverLocal) validate(cfg *Config) []errorstack.Entry {
	entries := unsupported(DriverLocal, "region", cfg.Region)
	entries = append(entries, unsupported(DriverLocal, "endpoint", cfg.Endpoint)...)
	entries = append(entries, unsupported(DriverLocal, "access_key_id", cfg.AccessKeyID)...)
	entries = append(entries, unsupported(DriverLocal, "secret_access_key", cfg.SecretAccessKey)...)

	return entries
}

// open opens the directory from its URL.
func (d driverLocal) open(ctx context.Context, cfg *Config) (*blob.Bucket, error) {
	return openURL(ctx, d, cfg)
}

// url returns the fileblob URL of the directory.
//
// The path is made absolute first. fileblob reads a relative path only from a
// URL whose host is ".", so formatting "file://" + a relative path made its first
// segment the host and opened the rest from the filesystem root: "data/uploads"
// became "/uploads".
func (driverLocal) url(cfg *Config) (*url.URL, error) {
	dir, err := filepath.Abs(cfg.Bucket)
	if err != nil {
		return nil, err
	}

	// A Windows path has no leading slash, and fileblob drops the one a URL path
	// always has before handing the path back to the filesystem.
	path := filepath.ToSlash(dir)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return &url.URL{
		Scheme: "file",
		Path:   path,
	}, nil
}
