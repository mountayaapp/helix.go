package bucket

import (
	"context"
	"net/url"
	"regexp"

	"github.com/mountayaapp/helix.go/errorstack"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/azureblob"
)

// DriverAzure uses Azure Blob Storage. Config.Bucket is the container.
//
// The storage account is named by AZURE_STORAGE_ACCOUNT, or by the connection
// string. Credentials are, in this order: a shared key (AZURE_STORAGE_KEY), a SAS
// token (AZURE_STORAGE_SAS_TOKEN), a connection string
// (AZURE_STORAGE_CONNECTION_STRING), and otherwise the Azure SDK's
// DefaultAzureCredential — environment service principal, workload identity,
// managed identity, then the Azure CLI. Nothing is required in the environment
// beyond the account name and what that chain needs.
//
// Config.Endpoint is the scheme and domain of the Blob service, for a sovereign
// cloud ("https://blob.core.usgovcloudapi.net") or the Azurite emulator
// ("http://127.0.0.1:10000"); the account name is still the one above.
// Config.Region, Config.AccessKeyID and Config.SecretAccessKey do not apply.
//
// Config example:
//
//	bucket.Config{
//	  Driver: bucket.DriverAzure,
//	  Bucket: "my-container",
//	}
const DriverAzure Driver = "azure"

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

// driverAzure is the behaviour behind DriverAzure.
type driverAzure struct{}

// validate ensures the container name is one Blob Storage accepts, that Endpoint
// names a service and nothing more, and that neither Region nor a key pair is
// set.
func (driverAzure) validate(cfg *Config) []errorstack.Entry {
	var entries []errorstack.Entry

	name := cfg.Bucket
	valid := azureReservedContainers[name] ||
		(len(name) >= 3 && len(name) <= 63 && azureContainerName.MatchString(name))

	if name != "" && !valid {
		entries = append(entries, errorstack.Entry{
			Message: "Must be 3 to 63 lowercase letters, digits or single hyphens, starting and ending with a letter or digit",
			Path:    []any{"config", "bucket"},
		})
	}

	if cfg.Endpoint != "" {
		u, ok := parseEndpoint(cfg.Endpoint)
		if !ok || (u.Path != "" && u.Path != "/") {
			entries = append(entries, errorstack.Entry{
				Message: "Must be an http or https URL of the service domain, without path, credentials, query or fragment",
				Path:    []any{"config", "endpoint"},
			})
		}
	}

	entries = append(entries, unsupported(DriverAzure, "region", cfg.Region)...)
	entries = append(entries, unsupported(DriverAzure, "access_key_id", cfg.AccessKeyID)...)
	entries = append(entries, unsupported(DriverAzure, "secret_access_key", cfg.SecretAccessKey)...)

	return entries
}

// open opens the container from its URL.
func (d driverAzure) open(ctx context.Context, cfg *Config) (*blob.Bucket, error) {
	return openURL(ctx, d, cfg)
}

// url returns the azureblob URL of the container. The driver builds the service
// URL from a protocol and a domain rather than from a URL, so that is how an
// Endpoint is handed to it.
func (driverAzure) url(cfg *Config) (*url.URL, error) {
	query := url.Values{}
	if cfg.Endpoint != "" {
		endpoint, err := url.Parse(cfg.Endpoint)
		if err != nil {
			return nil, err
		}

		query.Set("protocol", endpoint.Scheme)
		query.Set("domain", endpoint.Host)
	}

	return &url.URL{
		Scheme:   "azblob",
		Host:     cfg.Bucket,
		RawQuery: query.Encode(),
	}, nil
}
