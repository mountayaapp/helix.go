package bucket

import (
	"context"
	"io"

	"github.com/mountayaapp/helix.go/errorstack"

	"go.opentelemetry.io/otel/attribute"
	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
)

// DriverLocal uses a directory of the local filesystem. Config.Bucket is the
// directory, which must exist; a relative one is resolved against the working
// directory when Connect is called. It takes no field.
//
// Config example:
//
//	bucket.Config{
//	  Bucket: "/path/to/folder",
//	  Driver: bucket.DriverLocal{},
//	}
type DriverLocal struct{}

// name returns "local".
func (DriverLocal) name() string {
	return "local"
}

// validate accepts every directory: any path is a valid name, and whether it
// exists is for Connect to find out.
func (DriverLocal) validate(string) []errorstack.Entry {
	return nil
}

// attributes returns none: the directory is already the bucket.
func (DriverLocal) attributes() []attribute.KeyValue {
	return nil
}

// open opens the directory dir through fileblob, which resolves a relative one
// against the working directory. Nothing is formatted into a URL, so a directory
// name cannot reshape what is opened. The bucket holds nothing the connection has
// to close beside it.
func (DriverLocal) open(_ context.Context, dir string) (*blob.Bucket, io.Closer, error) {
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		return nil, nil, err
	}

	return b, nil, nil
}
