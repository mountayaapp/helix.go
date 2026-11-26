# helix.go - Bucket integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountaya/helix.go.svg)](https://pkg.go.dev/github.com/mountaya/helix.go/integration/bucket)
[![Latest version](https://img.shields.io/github/v/tag/mountaya/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountaya/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The bucket integration provides a standardized way to interact with blob storage
providers through drivers. It is a **dependency** integration: calling
`bucket.Connect(ctx, svc, cfg)` opens the bucket, checks that it can be listed, and
registers it with the Service through `service.Attach()`. The Service owns its
lifecycle from then on; the `bucket.Bucket` you get back can read and write blobs,
not close the bucket.

Supported drivers:
- [Amazon S3](https://aws.amazon.com/s3/), and S3-compatible services such as MinIO,
  Cloudflare R2 or LocalStack — `bucket.DriverS3`
- [Azure Blob Storage](https://azure.microsoft.com/products/storage/blobs) —
  `bucket.DriverAzure`
- [Google Cloud Storage](https://cloud.google.com/storage) —
  `bucket.DriverGoogleCloud`
- Local files — `bucket.DriverLocal`

## Installation

```sh
$ go get github.com/mountaya/helix.go/integration/bucket
```

## Configuration

- `Name` (`string`) — Identifies the connection in health output, error context and
  telemetry (`bucket.name`). Two buckets attached to the same Service must have
  different names. Default: `"bucket"`.
- `Driver` (`bucket.Driver`) — Storage provider, with everything needed to reach
  it: one of `bucket.DriverS3{…}`, `bucket.DriverAzure{…}`,
  `bucket.DriverGoogleCloud{…}` or `bucket.DriverLocal{}`, each holding the fields
  of its own provider and no other. **Required**.
- `Bucket` (`string`) — Bucket name: the container for Azure, the directory for
  the local driver. Checked against the provider's naming rules. **Required**.
- `Subfolder` (`string`) — Optional folder inside the bucket where every key is
  stored: operations on `"<key>"` are translated to `"<subfolder><key>"`. It is
  normalized: a leading `/` is dropped and a single trailing `/` is added, so
  `"my/subfolder"`, `"/my/subfolder"` and `"my/subfolder/"` are the same folder.
  Default: `""`, the bucket root (`"/"` is the root too).

A `Config` names one provider and configures it with that provider's fields
alone: a field of another provider does not compile. A validation error names the
field — `config.driver.region`, `config.driver.account_key` — never its value.

A `Config` encodes to JSON with its driver's own fields, credentials excluded. A
`"driver"` cannot be decoded from JSON: the driver is set in code, along with the
credentials it holds.

### `DriverS3`

- `Region` (`string`) — Region of the bucket, which every request is signed for.
  An S3-compatible service names its own — `"us-east-1"` for MinIO, `"auto"` for
  Cloudflare R2. **Required**.
- `Endpoint` (`string`) — URL of the service requests are sent to, when it is not
  Amazon S3: an S3-compatible service or a VPC endpoint, such as
  `"http://localhost:9000"`. Requests are path-style (the bucket in the URL path),
  which every S3-compatible service accepts.
- `AccessKeyID` (`string`) — Access key of the static key pair the requests are
  signed with. **Required**.
- `SecretAccessKey` (`string`) — Secret key of that key pair. **Required**.

### `DriverAzure`

- `AccountName` (`string`) — Name of the storage account: 3 to 24 lowercase
  letters or digits. **Required**.
- `AccountKey` (`string`) — One of the account's access keys, base64-encoded as
  Azure issues it. **Required**.
- `Endpoint` (`string`) — URL of the account's Blob service, used as given: a
  sovereign cloud (`"https://myaccount.blob.core.usgovcloudapi.net"`), a custom
  domain, or Azurite with the account in the path
  (`"http://127.0.0.1:10000/devstoreaccount1"`, under any host name). Default:
  `"https://<AccountName>.blob.core.windows.net"`.

### `DriverGoogleCloud`

- `CredentialsJSON` (`string`) — Key of the service account the requests are made
  as: the JSON key Google Cloud issues for it, whose `"type"` is
  `"service_account"`. **Required**.
- `Endpoint` (`string`) — Scheme and domain of the Cloud Storage service, with no
  path, when it is not the public one: an emulator such as fake-gcs-server
  (`"http://localhost:4443"`), or a Private Service Connect endpoint.

### `DriverLocal`

It takes no field. `Config.Bucket` is the directory, which must exist; a relative
one is resolved against the working directory when `Connect` is called.

### Credentials

Every cloud driver takes its static credential from its own fields:

- **S3** — the key pair `AccessKeyID` and `SecretAccessKey`, signed for
  `Region`.
- **Azure** — the storage account's shared key, `AccountName` and `AccountKey`.
- **Google Cloud** — the service account key in `CredentialsJSON`, exchanged for
  an access token at the key's `token_uri`.

Each client is built from its driver alone. No `AWS_*`, `AZURE_*` or `GOOGLE_*`
variable, no `STORAGE_EMULATOR_HOST`, shared configuration or credentials file,
Application Default Credentials, instance metadata, or managed or workload
identity is read, so neither the credentials nor the service the requests go to
can change behind the caller's back. Where a credential comes from — the
environment, a mounted file, a secret store — is the caller's to decide, under
names of its own. Workload identities — IAM roles for service accounts, GKE
Workload Identity, Azure managed identities — are therefore not supported.

A Cloud Storage emulator reached through `Endpoint` still receives requests
authorized with the key, whose token is fetched from the key's `token_uri`: that
exchange must succeed for the emulator to be reached.

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

  "github.com/mountaya/helix.go/integration/bucket"
  "github.com/mountaya/helix.go/service"
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
    Bucket:    "my-bucket",
    Subfolder: "path/to/subfolder/",
    Driver: bucket.DriverS3{
      Region:          "eu-west-1",
      AccessKeyID:     os.Getenv("UPLOADS_ACCESS_KEY_ID"),
      SecretAccessKey: os.Getenv("UPLOADS_SECRET_ACCESS_KEY"),
    },
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

An S3-compatible service is the S3 driver with an `Endpoint`:

```go
b, err := bucket.Connect(ctx, svc, bucket.Config{
  Bucket: "my-bucket",
  Driver: bucket.DriverS3{
    Region:          "us-east-1",
    Endpoint:        "http://localhost:9000",
    AccessKeyID:     os.Getenv("STORAGE_ACCESS_KEY_ID"),
    SecretAccessKey: os.Getenv("STORAGE_SECRET_ACCESS_KEY"),
  },
})
```

Azure Blob Storage, where `Bucket` is the container:

```go
b, err := bucket.Connect(ctx, svc, bucket.Config{
  Bucket: "my-container",
  Driver: bucket.DriverAzure{
    AccountName: "mystorageaccount",
    AccountKey:  os.Getenv("STORAGE_ACCOUNT_KEY"),
  },
})
```

Google Cloud Storage, with the service account key read from a mounted file:

```go
key, err := os.ReadFile("/var/secrets/storage/key.json")
if err != nil {
  return err
}

b, err := bucket.Connect(ctx, svc, bucket.Config{
  Bucket: "my-bucket",
  Driver: bucket.DriverGoogleCloud{
    CredentialsJSON: string(key),
  },
})
```

A local directory, where `Bucket` is the directory:

```go
b, err := bucket.Connect(ctx, svc, bucket.Config{
  Bucket: "./data/uploads",
  Driver: bucket.DriverLocal{},
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
- `bucket.driver` — `"s3"`, `"azure"`, `"google"` or `"local"`
- `bucket.bucket`
- `bucket.key`

When set, these are carried as well:
- `bucket.subfolder`
- `bucket.region` (S3)
- `bucket.endpoint`

A credential is never carried.

Example:
```
bucket.name: "uploads"
bucket.driver: "s3"
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

- The bucket name is checked against the provider's naming rules, and every client
  is built field by field from its driver. Nothing is formatted into a Go Cloud
  URL, so a `Bucket`, a `Region` or a directory name can never add a parameter of
  its own, such as another endpoint or anonymous access.
- Credentials are never encoded to JSON, stamped on a span or quoted in a
  validation error, and nothing in the environment can stand in for them or
  redirect requests. The standard HTTP proxy variables (`HTTPS_PROXY`,
  `NO_PROXY`) still apply, as for any Go HTTP client.
- An `Endpoint` with credentials, a query or a fragment is refused. An `http://`
  endpoint is plaintext: keep it to a local emulator.
- A relative directory for the local driver is resolved against the working
  directory when `Connect` is called.
