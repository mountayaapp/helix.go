# helix.go - Bucket integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/bucket)
[![Latest version](https://img.shields.io/github/v/tag/mountayaapp/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountayaapp/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The bucket integration provides a standardized way to interact with blob storage
providers through drivers. It is a **dependency** integration: calling
`bucket.Connect(ctx, svc, cfg)` opens the bucket, checks that it can be listed, and
registers it with the Service through `service.Attach()`. The Service owns its
lifecycle from then on; the `bucket.Bucket` you get back can read and write blobs,
not close the bucket.

Supported drivers:
- [AWS S3](https://aws.amazon.com/s3/), and S3-compatible services such as MinIO,
  Cloudflare R2 or LocalStack — `bucket.DriverAWS` (`"aws"`)
- [Azure Blob Storage](https://azure.microsoft.com/products/storage/blobs) —
  `bucket.DriverAzure` (`"azure"`)
- [Google Cloud Storage](https://cloud.google.com/storage) —
  `bucket.DriverGoogleCloud` (`"google"`)
- Local files — `bucket.DriverLocal` (`"local"`)

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/bucket
```

## Configuration

- `Name` (`string`) — Identifies the connection in health output, error context and
  telemetry (`bucket.name`). Two buckets attached to the same Service must have
  different names. Default: `"bucket"`.
- `Driver` (`bucket.Driver`) — Storage driver to use. In JSON, the driver's name:
  `"aws"`, `"azure"`, `"google"` or `"local"`. **Required**.
- `Bucket` (`string`) — Bucket name: the container for Azure, the directory for
  the local driver. Checked against the provider's naming rules. **Required**.
- `Subfolder` (`string`) — Optional folder inside the bucket where every key is
  stored: operations on `"<key>"` are translated to `"<subfolder><key>"`. It is
  normalized: a leading `/` is dropped and a single trailing `/` is added, so
  `"my/subfolder"`, `"/my/subfolder"` and `"my/subfolder/"` are the same folder.
  Default: `""`, the bucket root (`"/"` is the root too).
- `Region` (`string`) — Region of the bucket, for `DriverAWS` only. When empty,
  the AWS SDK resolves it: `AWS_REGION`, then `AWS_DEFAULT_REGION`, then the shared
  configuration profile. An S3-compatible service names its own — `"us-east-1"`
  for MinIO, `"auto"` for Cloudflare R2.
- `Endpoint` (`string`) — URL of the storage service, when it is not the
  provider's public one.
  - `DriverAWS`: the service requests are sent to — an S3-compatible service or a
    VPC endpoint — such as `"http://localhost:9000"`. Requests are path-style (the
    bucket in the URL path), which every S3-compatible service accepts.
  - `DriverAzure`: the scheme and domain of the Blob service, for a sovereign cloud
    (`"https://blob.core.usgovcloudapi.net"`) or Azurite
    (`"http://127.0.0.1:10000"`), with no path.
  - `DriverGoogleCloud` and `DriverLocal` refuse it. The Cloud Storage client
    reaches an emulator through `STORAGE_EMULATOR_HOST`.

A field a driver has no use for is refused rather than ignored: a `Region` or an
`Endpoint` set for the Google Cloud or local driver is a validation error.

### Credentials

No credential is ever set in `Config`, and none is required in the environment:
each driver uses its SDK's default credential chain, so workload identities work
without a static key.

- **AWS** — the AWS SDK's default chain: `AWS_ACCESS_KEY_ID` and
  `AWS_SECRET_ACCESS_KEY`, the shared configuration and credentials files
  (`AWS_PROFILE`), web identity (IAM Roles for Service Accounts on EKS), and the
  ECS or EC2 instance role.
- **Google Cloud** — Application Default Credentials: the file named by
  `GOOGLE_APPLICATION_CREDENTIALS`, the gcloud CLI's credentials, or the metadata
  server (GKE Workload Identity, the attached service account on Compute Engine
  and Cloud Run).
- **Azure** — the storage account is named by `AZURE_STORAGE_ACCOUNT`, or by the
  connection string. Credentials are, in this order: `AZURE_STORAGE_KEY`,
  `AZURE_STORAGE_SAS_TOKEN`, `AZURE_STORAGE_CONNECTION_STRING`, and otherwise
  `DefaultAzureCredential` (environment service principal, workload identity,
  managed identity, Azure CLI).

The identity needs permission to **list** the bucket (`s3:ListBucket`,
`storage.objects.list`, or listing blobs on Azure) as well as to read and write
blobs: `Connect` and the readiness probe both check that the bucket can be listed.

## Usage

### Connecting

```go
import (
  "context"
  "os"
  "os/signal"
  "syscall"

  "github.com/mountayaapp/helix.go/integration/bucket"
  "github.com/mountayaapp/helix.go/service"
)

func run() error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    return err
  }

  b, err := bucket.Connect(ctx, svc, bucket.Config{
    Name:      "uploads",
    Driver:    bucket.DriverAWS,
    Bucket:    "my-bucket",
    Region:    "eu-west-1",
    Subfolder: "path/to/subfolder/",
  })
  if err != nil {
    return err
  }

  // Hand b to the servers that use it. Its lifecycle belongs to svc.
  _ = b

  // Run blocks until ctx is cancelled, then closes the bucket along with the
  // rest of the Service.
  return svc.Run(ctx)
}
```

An S3-compatible service is the AWS driver with an `Endpoint`:

```go
b, err := bucket.Connect(ctx, svc, bucket.Config{
  Driver:   bucket.DriverAWS,
  Bucket:   "my-bucket",
  Region:   "us-east-1",
  Endpoint: "http://localhost:9000",
})
```

`Connect` returns:
- a `VALIDATION_FAILED` error, before anything is opened, when the `Config` is not
  valid;
- a `SERVICE_UNAVAILABLE` error — `"Failed to open bucket"` or `"Failed to reach
  bucket"` — when the driver cannot open the bucket or cannot list it within
  `ctx`: a missing bucket or directory, refused credentials, an unreachable
  endpoint. The driver's message is in the entry's `reason` extension.

### Reading

`Read` loads the whole blob into memory:

```go
data, err := b.Read(ctx, "blob.json")
if bucket.IsNotFound(err) {
  // There is no blob at "blob.json".
}
if err != nil {
  // ...
}
```

`NewReader` streams it. The caller must `Close` the reader:

```go
r, err := b.NewReader(ctx, "archive.tar")
if err != nil {
  // ...
}
defer r.Close()

_, err = io.Copy(dst, r)
```

### Writing

`Write` writes a blob from memory, replacing any blob already at the key:

```go
err := b.Write(ctx, "report.json", []byte(`{"status":"ok"}`), &bucket.OptionsWrite{
  ContentType: "application/json",
  Metadata: map[string]string{
    "generated-by": "my-service",
  },
})
```

`NewWriter` streams it. The blob is written only when `Close` returns `nil`:
until then, readers still see the previous blob, and an error from `Close` means
nothing was written. To abandon a write, cancel `ctx`, then `Close`:

```go
ctx, cancel := context.WithCancel(ctx)
defer cancel()

w, err := b.NewWriter(ctx, "archive.tar", &bucket.OptionsWrite{
  ContentType: "application/x-tar",
})
if err != nil {
  // ...
}

if _, err := io.Copy(w, src); err != nil {
  cancel() // Abandon the write rather than commit what was copied.
  w.Close()
  // ...
}

if err := w.Close(); err != nil {
  // Nothing was written.
}
```

`OptionsWrite` sets `CacheControl`, `ContentDisposition`, `ContentEncoding`,
`ContentLanguage`, `ContentType` (sniffed from the content when empty),
`ContentMD5` (checked against what was written) and `Metadata`.

### Checking and deleting

```go
exists, err := b.Exists(ctx, "report.json")

err = b.Delete(ctx, "report.json")
if bucket.IsNotFound(err) {
  // There was no blob at "report.json".
}
```

### Missing blobs

`Read`, `NewReader` and `Delete` report a missing blob as an error wrapping
`bucket.ErrNotFound`: test for it with `bucket.IsNotFound(err)` or
`errors.Is(err, bucket.ErrNotFound)`. `Exists` reports it as `false` with a `nil`
error. A missing blob is an answer rather than a failure, so it is not recorded as
an error on the span and is measured with the outcome `ok`.

## Health

`CheckHealth` lists the bucket, the same check `Connect` makes. When it fails, the
Service's readiness probe answers a generic `503` with no detail; the failure, with
the driver's message in its `reason`, goes to the log and the span.

## Telemetry

### Traces

Every operation is a `client` span named after the method:

- `Bucket: Exists`
- `Bucket: Read`
- `Bucket: NewReader` — ends when the reader is closed
- `Bucket: Write`
- `Bucket: NewWriter` — ends when the writer is closed
- `Bucket: Delete`

Every span carries:
- `bucket.name`
- `bucket.driver`
- `bucket.bucket`
- `bucket.key`

When set in the `Config`, these are carried as well:
- `bucket.subfolder`
- `bucket.region`
- `bucket.endpoint`

Example:
```
bucket.name: "uploads"
bucket.driver: "aws"
bucket.bucket: "my-bucket"
bucket.key: "blob.json"
bucket.subfolder: "path/to/subfolder/"
bucket.region: "eu-west-1"
```

The Go CDK, which the drivers are built on, opens a span of its own for each
operation through the global tracer provider. The Service suppresses that
instrumentation scope, so one operation is one span.

### Metrics

- `bucket.operation.duration` (histogram, seconds) — duration of every operation,
  with the attributes `bucket.name`, `bucket.operation` (`Exists`, `Read`,
  `NewReader`, `Write`, `NewWriter`, `Delete`) and `bucket.outcome` (`ok` or
  `error`). A streamed operation is measured until its reader or writer is closed.

The Go CDK also records `gocloud.dev/blob/latency`, `gocloud.dev/blob/bytes_read`
and `gocloud.dev/blob/bytes_written` through the global meter provider.

## Security

- The bucket name is checked against the provider's naming rules, and every
  driver URL is built field by field: a `Bucket` or `Region` value can never add a
  parameter of its own, such as another endpoint or anonymous access.
- An `Endpoint` with credentials, a query or a fragment is refused. An `http://`
  endpoint is plaintext: keep it to a local emulator.
- A relative directory for the local driver is resolved against the working
  directory when `Connect` is called.
