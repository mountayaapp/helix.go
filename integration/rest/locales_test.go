package rest

import (
	"net/http"
	"testing"

	"github.com/mountaya/helix.go/internal/locales"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/language"
)

// TestAddOrEditLanguage_DelegatesToSharedCatalog verifies that the package-level
// AddOrEditLanguage forwards to the internal shared catalog. The catalog
// behavior itself (matcher rebuilding, partial updates, cookie/header priority)
// is exhaustively tested in internal/locales — this test exists only to guard
// against accidental decoupling of the wrapper from the shared backing state.
func TestAddOrEditLanguage_DelegatesToSharedCatalog(t *testing.T) {
	lang := language.MustParse("xh")

	// The catalog is process-wide and has no way to remove a language. Blanking the
	// message is the closest thing: an empty message falls back to English, so no
	// later test sees the marker.
	t.Cleanup(func() {
		AddOrEditLanguage(lang, map[int]string{http.StatusBadRequest: ""})
	})

	AddOrEditLanguage(lang, map[int]string{
		http.StatusBadRequest: "rest-wrapper-marker",
	})

	assert.Equal(t, "rest-wrapper-marker", locales.Message(httpReqWithHeader("xh"), http.StatusBadRequest))
}

func httpReqWithHeader(lang string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", lang)
	return req
}
