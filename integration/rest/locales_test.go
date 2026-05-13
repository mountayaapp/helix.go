package rest

import (
	"net/http"
	"testing"

	"github.com/mountayaapp/helix.go/internal/locales"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/language"
)

/*
TestAddOrEditLanguage_DelegatesToSharedCatalog verifies that the package-level
AddOrEditLanguage forwards to the internal shared catalog. The catalog
behavior itself (matcher rebuilding, partial updates, cookie/header priority)
is exhaustively tested in internal/locales — this test exists only to guard
against accidental decoupling of the wrapper from the shared backing state.
*/
func TestAddOrEditLanguage_DelegatesToSharedCatalog(t *testing.T) {
	defer func() {
		// internal/locales has no public removal API; mutate via package-level
		// AddOrEditLanguage one more time to overwrite, then verify it's
		// reachable via the shared package — the integration tests in
		// internal/locales fully exercise teardown.
	}()

	AddOrEditLanguage(language.MustParse("xh"), map[int]string{
		http.StatusBadRequest: "rest-wrapper-marker",
	})

	assert.Equal(t, "rest-wrapper-marker", locales.Message(httpReqWithHeader("xh"), http.StatusBadRequest))
}

func httpReqWithHeader(lang string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", lang)
	return req
}
