package bucket

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// newFakeAzure starts a Blob service fakeStore answering the requests Connect,
// CheckHealth and Exists make: listing the container, and asking for a blob,
// which it never holds.
func newFakeAzure(t *testing.T) *fakeStore {
	t.Helper()

	return newFakeStore(t, func(rw http.ResponseWriter, req *http.Request, status int) {
		// A HEAD is Exists asking for a blob, which the fake never holds.
		if req.Method == http.MethodHead {
			rw.Header().Set("x-ms-error-code", "BlobNotFound")
			rw.WriteHeader(http.StatusNotFound)
			return
		}

		rw.Header().Set("Content-Type", "application/xml")

		switch status {
		case http.StatusOK:
			// The listing must hold a Blobs element, even an empty one: the driver
			// reads the segment it stands for unconditionally.
			fmt.Fprint(rw, `<?xml version="1.0" encoding="utf-8"?>`+
				`<EnumerationResults ContainerName="my-container">`+
				`<MaxResults>1</MaxResults><Blobs></Blobs><NextMarker></NextMarker>`+
				`</EnumerationResults>`)
		case http.StatusNotFound:
			rw.Header().Set("x-ms-error-code", "ContainerNotFound")
			rw.WriteHeader(status)
			fmt.Fprint(rw, `<?xml version="1.0" encoding="utf-8"?>`+
				`<Error><Code>ContainerNotFound</Code><Message>The specified container does not exist.</Message></Error>`)
		default:
			rw.Header().Set("x-ms-error-code", "AuthenticationFailed")
			rw.WriteHeader(status)
			fmt.Fprint(rw, `<?xml version="1.0" encoding="utf-8"?>`+
				`<Error><Code>AuthenticationFailed</Code><Message>Server failed to authenticate the request.</Message></Error>`)
		}
	})
}

// setHostileAzureEnv sets every variable Go Cloud's Azure URL opener reads an
// account, a credential or a service from — the account and its key, a SAS token,
// both connection strings, and the domain, protocol, emulator and CDN switches —
// to values of their own, which no driver of a test names. The service they name
// is a decoy: DriverAzure must follow none of them.
func setHostileAzureEnv(t *testing.T) {
	t.Helper()

	decoy := newDecoy(t)
	key := base64.StdEncoding.EncodeToString([]byte("from-env"))
	connection := "DefaultEndpointsProtocol=http;AccountName=fromenv;AccountKey=" + key +
		";BlobEndpoint=" + decoy.URL + "/fromenv;"

	for name, value := range map[string]string{
		"AZURE_STORAGE_ACCOUNT":              "fromenv",
		"AZURE_STORAGE_KEY":                  key,
		"AZURE_STORAGE_SAS_TOKEN":            "sv=2024-01-01&sig=from-env",
		"AZURE_STORAGE_CONNECTION_STRING":    connection,
		"AZURE_STORAGEBLOB_CONNECTIONSTRING": connection,
		"AZURE_STORAGE_DOMAIN":               strings.TrimPrefix(decoy.URL, "http://"),
		"AZURE_STORAGE_PROTOCOL":             "http",
		"AZURE_STORAGE_IS_LOCAL_EMULATOR":    "true",
		"AZURE_STORAGE_IS_CDN":               "true",
	} {
		t.Setenv(name, value)
	}
}

// azureDriver returns a valid DriverAzure of the account "configaccount",
// reaching endpoint — the account's public service when it is empty.
func azureDriver(endpoint string) DriverAzure {
	return DriverAzure{
		AccountName: "configaccount",
		AccountKey:  base64.StdEncoding.EncodeToString([]byte("config-key")),
		Endpoint:    endpoint,
	}
}
