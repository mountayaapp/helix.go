package bucket

import (
	"fmt"
	"os"

	"github.com/mountayaapp/helix.go/errorstack"

	_ "gocloud.dev/blob/azureblob"
)

/*
DriverAzure allows to use Azure Blob Storage as bucket driver. This driver relies
on generic environment variables required by the Azure SDK:

  - AZURE_STORAGE_ACCOUNT
  - AZURE_STORAGE_KEY || AZURE_STORAGE_SAS_TOKEN

Config example:

	bucket.Config{
	  Driver: bucket.DriverAzure,
	  Bucket: "my-container",
	}
*/
var DriverAzure Driver = &driverAzure{}

/*
driverAzure is the internal type to use Azure as bucket driver.
*/
type driverAzure struct{}

/*
string returns the string representation of the Azure bucket driver.
*/
func (d *driverAzure) string() string {
	return "azure"
}

/*
validate ensures Config and environment variables are valid for the Azure bucket
driver.
*/
func (d *driverAzure) validate(cfg *Config) []errorstack.Entry {
	var entries []errorstack.Entry

	if os.Getenv("AZURE_STORAGE_ACCOUNT") == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"env", "azure_storage_account"},
		})
	}

	if os.Getenv("AZURE_STORAGE_KEY") == "" && os.Getenv("AZURE_STORAGE_SAS_TOKEN") == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be one of: azure_storage_key, azure_storage_sas_token",
			Path:    []any{"env"},
		})
	}

	return entries
}

/*
url returns the Go Cloud bucket URL of the Azure bucket driver.
*/
func (d *driverAzure) url(cfg *Config) string {
	path := fmt.Sprintf("azblob://%s", cfg.Bucket)

	return path
}
