package graphql

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
	AddOrEditLanguage(language.MustParse("zu"), map[int]string{
		http.StatusBadRequest: "graphql-wrapper-marker",
	})

	assert.Equal(t, "graphql-wrapper-marker", locales.Message(httpReqWithHeader("zu"), http.StatusBadRequest))
}

func httpReqWithHeader(lang string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", lang)
	return req
}
