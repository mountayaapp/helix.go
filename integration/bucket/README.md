# helix.go - Bucket integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/bucket)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/bucket)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/bucket)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The bucket integration provides a standardized way to interact with blob storage
providers through drivers. It is a **dependency** integration — calling
`bucket.Connect(svc, cfg)` automatically registers it via `service.Attach()`.

Supported drivers:
- [AWS S3](https://aws.amazon.com/s3/) — `bucket.DriverAWS`
- [Azure Blob Storage](https://azure.microsoft.com/products/storage/blobs) — `bucket.DriverAzure`
- [Google Cloud Storage](https://cloud.google.com/storage) — `bucket.DriverGoogleCloud`
- Local files — `bucket.DriverLocal`

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/bucket
```

## Configuration

- `Driver` (`bucket.Driver`) — Storage driver to use. **Required**.
- `Bucket` (`string`) — Bucket or container name. **Required**.
- `Subfolder` (`string`) — Optional key prefix. Operations on `"<key>"` are
  translated to `"<subfolder><key>"`. Default: `"/"`.

## Usage

### Connecting

```go
import (
  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/bucket"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

b, err := bucket.Connect(svc, bucket.Config{
  Driver:    bucket.DriverAWS,
  Bucket:    "my-bucket",
  Subfolder: "path/to/subfolder/",
})
if err != nil {
  panic(err)
}
```

### Reading

```go
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

### Writing

```go
err = b.Write(ctx, "report.json", []byte(`{"status":"ok"}`), &bucket.OptionsWrite{
  ContentType: "application/json",
  Metadata: map[string]string{
    "generated-by": "my-service",
  },
})
if err != nil {
  // ...
}
```

### Deleting

```go
err = b.Delete(ctx, "report.json")
if err != nil {
  // ...
}
```

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
