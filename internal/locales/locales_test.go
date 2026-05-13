package locales

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"
)

/*
withFrenchTearDown registers French via AddOrEditLanguage and returns a
cleanup function that removes it. Tests use this to keep the package-level
catalog pristine across runs.
*/
func withFrenchTearDown(t *testing.T) func() {
	t.Helper()
	originalLen := len(languages)

	return func() {
		mu.Lock()
		defer mu.Unlock()
		delete(catalog, language.French)
		languages = languages[:originalLen]
		matcher = language.NewMatcher(languages)
	}
}

func TestGetPreferredLanguage(t *testing.T) {
	testcases := []struct {
		name     string
		cookie   string
		header   string
		expected language.Tag
	}{
		{name: "no header", expected: language.English},
		{name: "english header", header: "en", expected: language.English},
		{name: "unsupported language", header: "fr", expected: language.English},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Accept-Language", tc.header)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "lang", Value: tc.cookie})
			}

			assert.Equal(t, tc.expected, GetPreferredLanguage(req))
		})
	}
}

func TestGetPreferredLanguage_NilRequest(t *testing.T) {
	assert.Equal(t, language.English, GetPreferredLanguage(nil))
}

func TestAddOrEditLanguage_NewLanguage(t *testing.T) {
	originalLen := len(languages)
	defer withFrenchTearDown(t)()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest:          "Requête invalide",
		http.StatusInternalServerError: "Erreur interne du serveur",
	})

	mu.RLock()
	defer mu.RUnlock()
	assert.Equal(t, "Requête invalide", catalog[language.French][http.StatusBadRequest])
	assert.Equal(t, "Erreur interne du serveur", catalog[language.French][http.StatusInternalServerError])
	assert.Equal(t, originalLen+1, len(languages))
}

func TestAddOrEditLanguage_EditExistingLanguage(t *testing.T) {
	mu.Lock()
	original := catalog[language.English][http.StatusNotFound]
	mu.Unlock()
	defer func() {
		mu.Lock()
		catalog[language.English][http.StatusNotFound] = original
		mu.Unlock()
	}()

	AddOrEditLanguage(language.English, map[int]string{
		http.StatusNotFound: "Custom not found message",
	})

	mu.RLock()
	defer mu.RUnlock()
	assert.Equal(t, "Custom not found message", catalog[language.English][http.StatusNotFound])
	assert.Equal(t, "Authentication is required", catalog[language.English][http.StatusUnauthorized])
}

func TestGetPreferredLanguage_WithAddedLanguage(t *testing.T) {
	defer withFrenchTearDown(t)()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "fr")

	assert.Equal(t, language.French, GetPreferredLanguage(req))
}

func TestGetPreferredLanguage_CookieAndHeader(t *testing.T) {
	defer withFrenchTearDown(t)()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "fr")
	req.AddCookie(&http.Cookie{Name: "lang", Value: "en"})

	assert.Equal(t, language.English, GetPreferredLanguage(req))
}

func TestAddOrEditLanguage_PartialUpdate(t *testing.T) {
	defer withFrenchTearDown(t)()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest:          "Requête invalide",
		http.StatusInternalServerError: "Erreur interne du serveur",
	})

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusNotFound: "Ressource introuvable",
	})

	mu.RLock()
	defer mu.RUnlock()
	assert.Equal(t, "Requête invalide", catalog[language.French][http.StatusBadRequest])
	assert.Equal(t, "Erreur interne du serveur", catalog[language.French][http.StatusInternalServerError])
	assert.Equal(t, "Ressource introuvable", catalog[language.French][http.StatusNotFound])
}

func TestAddOrEditLanguage_RebuildsMatcher(t *testing.T) {
	defer withFrenchTearDown(t)()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "fr")

	assert.Equal(t, language.English, GetPreferredLanguage(req))

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	assert.Equal(t, language.French, GetPreferredLanguage(req))
}

func TestSupportedLocales_AllEnglishStatusCodes(t *testing.T) {
	expectedCodes := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	}

	mu.RLock()
	defer mu.RUnlock()
	for _, code := range expectedCodes {
		msg, exists := catalog[language.English][code]
		assert.True(t, exists, "missing locale for status %d", code)
		assert.NotEmpty(t, msg, "empty locale for status %d", code)
	}
}

func TestGetPreferredLanguage_CookiePriority(t *testing.T) {
	defer withFrenchTearDown(t)()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en")
	req.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})

	assert.Equal(t, language.French, GetPreferredLanguage(req))
}

/*
TestLocales_MatchCanonicalCatalog pins every English message to its expected
value, ensuring the wire shape stays consistent with the OpenAPI examples in
documents/openapi/components/schemas/rest/{status}.yaml.
*/
func TestLocales_MatchCanonicalCatalog(t *testing.T) {
	canonical := map[int]string{
		http.StatusBadRequest:            "Request is invalid",
		http.StatusUnauthorized:          "Authentication is required",
		http.StatusPaymentRequired:       "Payment is required",
		http.StatusForbidden:             "Access is forbidden",
		http.StatusNotFound:              "Resource does not exist",
		http.StatusMethodNotAllowed:      "Method is not allowed for this resource",
		http.StatusConflict:              "Resource conflicts with current state",
		http.StatusRequestEntityTooLarge: "Payload exceeds size limit",
		http.StatusTooManyRequests:       "Rate limit has been exceeded",
		http.StatusInternalServerError:   "Internal server error",
		http.StatusNotImplemented:        "Endpoint is not implemented",
		http.StatusBadGateway:            "Upstream gateway is unavailable",
		http.StatusServiceUnavailable:    "Service is temporarily unavailable",
	}

	for status, expected := range canonical {
		t.Run(http.StatusText(status), func(t *testing.T) {
			actual := Message(nil, status)
			require.NotEmpty(t, actual)
			assert.Equal(t, expected, actual)
			assert.NotEqual(t, '.', actual[len(actual)-1])
			first := actual[0]
			assert.True(t, first >= 'A' && first <= 'Z', "must start with uppercase")
		})
	}
}

func TestMessage_FallsBackToEnglishWhenLanguageMissing(t *testing.T) {
	defer withFrenchTearDown(t)()

	// French registered but with no entry for 401 — Message must fall back to
	// English so the wire shape still carries a non-empty message.
	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "fr")

	assert.Equal(t, "Authentication is required", Message(req, http.StatusUnauthorized))
}
