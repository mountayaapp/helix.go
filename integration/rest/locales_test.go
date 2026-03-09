package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/language"
)

func TestGetPreferredLanguage(t *testing.T) {
	testcases := []struct {
		name     string
		cookie   string
		header   string
		expected language.Tag
	}{
		{
			name:     "no header",
			expected: language.English,
		},
		{
			name:     "english header",
			header:   "en",
			expected: language.English,
		},
		{
			name:     "unsupported language",
			header:   "fr",
			expected: language.English,
		},
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

			actual := getPreferredLanguage(req)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestGetPreferredLanguage_NilRequest(t *testing.T) {
	actual := getPreferredLanguage(nil)

	assert.Equal(t, language.English, actual)
}

func TestAddOrEditLanguage_NewLanguage(t *testing.T) {
	originalLen := len(supportedLanguages)
	defer func() {
		delete(supportedLocales, language.French)
		supportedLanguages = supportedLanguages[:originalLen]
		supportedMatcher = language.NewMatcher(supportedLanguages)
	}()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest:          "Requête invalide",
		http.StatusInternalServerError: "Erreur interne du serveur",
	})

	assert.Equal(t, "Requête invalide", supportedLocales[language.French][http.StatusBadRequest])
	assert.Equal(t, "Erreur interne du serveur", supportedLocales[language.French][http.StatusInternalServerError])
	assert.Equal(t, originalLen+1, len(supportedLanguages))
}

func TestAddOrEditLanguage_EditExistingLanguage(t *testing.T) {
	original := supportedLocales[language.English][http.StatusNotFound]
	defer func() {
		supportedLocales[language.English][http.StatusNotFound] = original
	}()

	AddOrEditLanguage(language.English, map[int]string{
		http.StatusNotFound: "Custom not found message",
	})

	assert.Equal(t, "Custom not found message", supportedLocales[language.English][http.StatusNotFound])
	assert.Equal(t, "You are not authorized to perform this action", supportedLocales[language.English][http.StatusUnauthorized])
}

func TestGetPreferredLanguage_WithAddedLanguage(t *testing.T) {
	originalLen := len(supportedLanguages)
	defer func() {
		delete(supportedLocales, language.French)
		supportedLanguages = supportedLanguages[:originalLen]
		supportedMatcher = language.NewMatcher(supportedLanguages)
	}()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "fr")

	actual := getPreferredLanguage(req)

	assert.Equal(t, language.French, actual)
}

func TestGetPreferredLanguage_CookieAndHeader(t *testing.T) {
	originalLen := len(supportedLanguages)
	defer func() {
		delete(supportedLocales, language.French)
		supportedLanguages = supportedLanguages[:originalLen]
		supportedMatcher = language.NewMatcher(supportedLanguages)
	}()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "fr")
	req.AddCookie(&http.Cookie{Name: "lang", Value: "en"})

	actual := getPreferredLanguage(req)

	assert.Equal(t, language.English, actual)
}

func TestAddOrEditLanguage_PartialUpdate(t *testing.T) {
	originalLen := len(supportedLanguages)
	defer func() {
		delete(supportedLocales, language.French)
		supportedLanguages = supportedLanguages[:originalLen]
		supportedMatcher = language.NewMatcher(supportedLanguages)
	}()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest:          "Requête invalide",
		http.StatusInternalServerError: "Erreur interne du serveur",
	})

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusNotFound: "Ressource introuvable",
	})

	assert.Equal(t, "Requête invalide", supportedLocales[language.French][http.StatusBadRequest])
	assert.Equal(t, "Erreur interne du serveur", supportedLocales[language.French][http.StatusInternalServerError])
	assert.Equal(t, "Ressource introuvable", supportedLocales[language.French][http.StatusNotFound])
}

func TestAddOrEditLanguage_RebuildsMatcher(t *testing.T) {
	originalLen := len(supportedLanguages)
	defer func() {
		delete(supportedLocales, language.French)
		supportedLanguages = supportedLanguages[:originalLen]
		supportedMatcher = language.NewMatcher(supportedLanguages)
	}()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "fr")

	// Before adding French, should fallback to English.
	assert.Equal(t, language.English, getPreferredLanguage(req))

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	// After adding French, cached matcher should resolve French.
	assert.Equal(t, language.French, getPreferredLanguage(req))
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

	for _, code := range expectedCodes {
		msg, exists := supportedLocales[language.English][code]
		assert.True(t, exists, "missing locale for status %d", code)
		assert.NotEmpty(t, msg, "empty locale for status %d", code)
	}
}

func TestGetPreferredLanguage_CookiePriority(t *testing.T) {
	originalLen := len(supportedLanguages)
	defer func() {
		delete(supportedLocales, language.French)
		supportedLanguages = supportedLanguages[:originalLen]
		supportedMatcher = language.NewMatcher(supportedLanguages)
	}()

	AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest: "Requête invalide",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en")
	req.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})

	actual := getPreferredLanguage(req)

	assert.Equal(t, language.French, actual)
}
