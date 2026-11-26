package bucket

import (
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"regexp"

	"github.com/mountaya/helix.go/errorstack"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"go.opentelemetry.io/otel/attribute"
	"gocloud.dev/blob"
	"gocloud.dev/blob/azureblob"
)

// DriverAzure uses Azure Blob Storage. Config.Bucket is the container.
//
// The requests are signed with the storage account's shared key, AccountName and
// AccountKey, and go to Endpoint, or to the account's public Blob service when it
// is empty. The client is built from these fields alone. Nothing is read from the
// environment: no AZURE_STORAGE_* variable, connection string,
// DefaultAzureCredential, or managed or workload identity, so neither the
// credentials nor the service the requests go to can change behind the caller's
// back.
//
// Config example:
//
//	bucket.Config{
//	  Bucket: "my-container",
//	  Driver: bucket.DriverAzure{
//	    AccountName: "mystorageaccount",
//	    AccountKey:  os.Getenv("MY_ACCOUNT_KEY"),
//	  },
//	}
type DriverAzure struct {

	// AccountName is the name of the storage account the shared key belongs to. It
	// is never encoded to JSON, nor quoted in a validation error.
	//
	// Required.
	//
	// Example:
	//
	//   "mystorageaccount"
	AccountName string `json:"-"`

	// AccountKey is one of the storage account's access keys, base64-encoded as
	// Azure issues it. It is never encoded to JSON, nor quoted in a validation
	// error.
	//
	// Required.
	AccountKey string `json:"-"`

	// Endpoint is the URL of the account's Blob service, used as given, when it is
	// not the public one: a sovereign cloud, a custom domain, or the Azurite
	// emulator, whose URL carries the account in its path.
	//
	// Default:
	//
	//   "https://<AccountName>.blob.core.windows.net"
	//
	// Examples:
	//
	//   "https://mystorageaccount.blob.core.usgovcloudapi.net"
	//   "http://127.0.0.1:10000/devstoreaccount1"
	Endpoint string `json:"endpoint,omitempty"`
}

// azureContainerName is the Blob Storage naming rule for a container: lowercase
// letters and digits, with hyphens that are never leading, trailing or doubled.
// The 3 to 63 character length is checked apart, being awkward to express here.
var azureContainerName = regexp.MustCompile(`^[a-z0-9](?:-?[a-z0-9])*$`)

// azureReservedContainers are the containers Azure names itself, which the rule
// above would refuse: the root container, and the ones holding a static website
// and the storage analytics logs.
var azureReservedContainers = map[string]bool{
	"$root": true,
	"$web":  true,
	"$logs": true,
}

// azureAccountName is the Storage naming rule for an account: 3 to 24 lowercase
// letters and digits. It also keeps the account name a single host label of the
// default service URL.
var azureAccountName = regexp.MustCompile(`^[a-z0-9]{3,24}$`)

// name returns "azure".
func (DriverAzure) name() string {
	return "azure"
}

// validate ensures the container name is one Blob Storage accepts, that the
// account name and key are set and well-formed, and that Endpoint, when set, is a
// URL requests can be sent to.
func (d DriverAzure) validate(bucket string) []errorstack.Entry {
	var entries []errorstack.Entry

	valid := azureReservedContainers[bucket] ||
		(len(bucket) >= 3 && len(bucket) <= 63 && azureContainerName.MatchString(bucket))

	if bucket != "" && !valid {
		entries = append(entries, errorstack.Entry{
			Message: "Must be 3 to 63 lowercase letters, digits or single hyphens, starting and ending with a letter or digit",
			Path:    []any{"config", "bucket"},
		})
	}

	entries = append(entries, required("account_name", d.AccountName)...)
	if d.AccountName != "" && !azureAccountName.MatchString(d.AccountName) {
		entries = append(entries, errorstack.Entry{
			Message: "Must be 3 to 24 lowercase letters or digits",
			Path:    driverPath("account_name"),
		})
	}

	entries = append(entries, required("account_key", d.AccountKey)...)
	if d.AccountKey != "" {
		if _, err := base64.StdEncoding.DecodeString(d.AccountKey); err != nil {
			entries = append(entries, errorstack.Entry{
				Message: "Must be a base64-encoded key",
				Path:    driverPath("account_key"),
			})
		}
	}

	return append(entries, validateEndpoint(d.Endpoint)...)
}

// attributes returns the endpoint, when set.
func (d DriverAzure) attributes() []attribute.KeyValue {
	return endpointAttributes(d.Endpoint)
}

// open opens the container through a client built from d alone
// (newContainerClient). The client holds nothing the connection has to close.
func (d DriverAzure) open(ctx context.Context, bucket string) (*blob.Bucket, io.Closer, error) {
	client, err := newContainerClient(d, bucket)
	if err != nil {
		return nil, nil, err
	}

	b, err := azureblob.OpenBucket(ctx, client, nil)
	if err != nil {
		return nil, nil, err
	}

	return b, nil, nil
}

// newContainerClient returns the client of the container named name that d
// describes, built from d and from nothing else: the shared key of the account,
// and the container's URL under Endpoint, or under the account's public Blob
// service. Endpoint is used as given rather than guessed from its host name, so
// that Azurite is reached under any host, its account in the path.
//
// The container is joined to the service URL as a path segment, so that its name
// cannot carry a query of its own into the URL.
func newContainerClient(d DriverAzure, name string) (*container.Client, error) {
	cred, err := container.NewSharedKeyCredential(d.AccountName, d.AccountKey)
	if err != nil {
		return nil, err
	}

	service := d.Endpoint
	if service == "" {
		service = (&url.URL{Scheme: "https", Host: d.AccountName + ".blob.core.windows.net"}).String()
	}

	containerURL, err := url.JoinPath(service, name)
	if err != nil {
		return nil, err
	}

	return container.NewClientWithSharedKeyCredential(containerURL, cred, nil)
}
