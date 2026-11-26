package bucket

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// configServiceAccount is the service account every DriverGoogleCloud of a test
// is configured with.
const configServiceAccount = "config@helix.iam.gserviceaccount.com"

// testRSAKey is the private key of every service account key a test builds,
// generated once: generating one is slow, and the tests tell accounts apart by
// their email, never by their key.
var testRSAKey = sync.OnceValues(func() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
})

// serviceAccountKey returns the JSON key of the service account email, whose
// token is fetched from tokenURI.
func serviceAccountKey(t *testing.T, email, tokenURI string) string {
	t.Helper()

	key, err := testRSAKey()
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	return encodeKey(t, map[string]string{
		"type":           "service_account",
		"project_id":     "helix-test",
		"private_key_id": "test-key",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   email,
		"token_uri":      tokenURI,
	})
}

// encodeKey returns the fields of a key file encoded as JSON.
func encodeKey(t *testing.T, fields map[string]string) string {
	t.Helper()

	encoded, err := json.Marshal(fields)
	require.NoError(t, err)

	return string(encoded)
}

// newFakeGCS starts a Cloud Storage fakeStore serving both the token endpoint a
// key's token_uri names — at "/token" — and the JSON API listing a bucket. The
// token it issues for an assertion is "token-for-<email>", the email being the
// assertion's issuer, so that a request's Authorization tells which key it was
// authorized with.
func newFakeGCS(t *testing.T) *fakeStore {
	t.Helper()

	return newFakeStore(t, func(rw http.ResponseWriter, req *http.Request, status int) {
		rw.Header().Set("Content-Type", "application/json")

		if req.URL.Path == "/token" {
			issuer, err := assertionIssuer(req.FormValue("assertion"))
			if err != nil {
				t.Errorf("the token request carries no readable assertion: %v", err)
				rw.WriteHeader(http.StatusBadRequest)
				return
			}

			fmt.Fprintf(rw, `{"access_token":"token-for-%s","token_type":"Bearer","expires_in":3600}`, issuer)
			return
		}

		// A request for one object is Exists asking for a blob, which the fake
		// never holds.
		if strings.Contains(req.URL.Path, "/o/") {
			rw.WriteHeader(http.StatusNotFound)
			fmt.Fprint(rw, `{"error":{"code":404,"message":"No such object."}}`)
			return
		}

		switch status {
		case http.StatusOK:
			fmt.Fprint(rw, `{"kind":"storage#objects"}`)
		default:
			rw.WriteHeader(status)
			fmt.Fprintf(rw, `{"error":{"code":%d,"message":"%s"}}`, status, http.StatusText(status))
		}
	})
}

// assertionIssuer returns the issuer of a JWT assertion, read from its claims
// without checking its signature.
func assertionIssuer(assertion string) (string, error) {
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("want 3 JWT segments, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}

	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}

	return claims.Issuer, nil
}

// setHostileGoogleEnv sets every source the Google libraries read credentials,
// an endpoint or a metadata server from to values of their own, which no driver
// of a test names:
//   - Application Default Credentials, a key of another account whose token comes
//     from the decoy;
//   - the emulator and the metadata server, both at the decoy;
//   - a universe domain and the mTLS switch;
//   - client metrics, which would look up both credentials and metadata.
//
// DriverGoogleCloud must follow none of them. STORAGE_EMULATOR_HOST takes the
// library down another path, so a test that wants the default one unsets it
// afterwards.
func setHostileGoogleEnv(t *testing.T) {
	t.Helper()

	decoy := newDecoy(t)
	host := strings.TrimPrefix(decoy.URL, "http://")

	credentials := filepath.Join(t.TempDir(), "credentials.json")
	require.NoError(t, os.WriteFile(credentials,
		[]byte(serviceAccountKey(t, "env@hostile.iam.gserviceaccount.com", decoy.URL+"/token")), 0o600))

	for name, value := range map[string]string{
		"GOOGLE_APPLICATION_CREDENTIALS":           credentials,
		"STORAGE_EMULATOR_HOST":                    host,
		"GCE_METADATA_HOST":                        host,
		"GOOGLE_CLOUD_UNIVERSE_DOMAIN":             "hostile.example",
		"GOOGLE_API_USE_MTLS_ENDPOINT":             "always",
		"GCP_STORAGE_GO_ENABLE_OTEL_METRICS":       "true",
		"GCP_STORAGE_GO_ENABLE_OTEL_DEBUG_METRICS": "true",
	} {
		t.Setenv(name, value)
	}
}

// googleDriver returns a valid DriverGoogleCloud of configServiceAccount reaching
// endpoint — Cloud Storage itself when it is empty — whose token comes from the
// endpoint's "/token".
func googleDriver(t *testing.T, endpoint string) DriverGoogleCloud {
	t.Helper()

	return DriverGoogleCloud{
		CredentialsJSON: serviceAccountKey(t, configServiceAccount, endpoint+"/token"),
		Endpoint:        endpoint,
	}
}
