# helix.go - Bucket integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/bucket)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/bucket)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/bucket)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The bucket integration provides an opinionated and standardized way to interact
with bucket providers through drivers. It is a **dependency** integration
registered via `service.Attach()`.

The integration supports the following drivers:
- [AWS S3](https://aws.amazon.com/s3/)
- [Azure Blob Storage](https://azure.microsoft.com/products/storage/blobs)
- [Google Cloud Storage](https://cloud.google.com/storage)
- Local files

## Trace attributes

The `bucket` integration sets the following trace attributes:
- `bucket.bucket`
- `bucket.driver`
- `bucket.key`

When applicable, these attributes can be set as well:
- `bucket.subfolder`

Example:
```
bucket.driver: "aws"
bucket.bucket: "my-bucket"
bucket.key: "blob.json"
bucket.subfolder: "path/to/subfolder/"
```

## Usage

Install the Go module with:
```sh
$ go get github.com/mountayaapp/helix.go/integration/bucket
```

Simple example on how to import, configure, and use the integration:

```go
import (
  "context"
  "encoding/json"

  "github.com/mountayaapp/helix.go/integration/bucket"
)

cfg := bucket.Config{
  Driver:    bucket.DriverAWS,
  Bucket:    "my-bucket",
  Subfolder: "path/to/subfolder/",
}

b, err := bucket.Connect(cfg)
if err != nil {
  return err
}

ctx := context.Background()
blob, err := b.Read(ctx, "blob.json")
if err != nil {
  // ...
}

var data map[string]any
err = json.Unmarshal(blob, &data)
if err != nil {
  // ...
}
```
