/*
Package locales centralizes the canonical HTTP-status → message catalog used by
both the REST and GraphQL integrations. Both transports are thin wrappers
around the same backing state, so a service that adds or edits a locale via
either rest.AddOrEditLanguage or graphql.AddOrEditLanguage sees the change
applied across both error paths — no silent divergence between the two.
*/
package locales

import (
	"maps"
	"net/http"
	"sync"

	"golang.org/x/text/language"
)

/*
defaults are the canonical English messages emitted for each supported HTTP
status code. They mirror the example: blocks of
documents/openapi/components/schemas/rest/{status}.yaml character-for-character
so the wire shape always matches the OpenAPI examples.

Style: Sentence case, no trailing period, present tense, actionable.
*/
var defaults = map[int]string{
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

/*
mu guards the package-level catalog and matcher. AddOrEditLanguage is expected
to be called at init time before serving, but locking keeps the package safe
against accidental concurrent edits and against reads racing a late edit.
*/
var (
	mu           sync.RWMutex
	languages    = []language.Tag{language.English}
	matcher      = language.NewMatcher(languages)
	catalog      = map[language.Tag]map[int]string{language.English: cloneDefaults()}
)

func cloneDefaults() map[int]string {
	return maps.Clone(defaults)
}

/*
AddOrEditLanguage adds or replaces the message catalog for a given language.
Previously-set entries for the same language are preserved unless the new
catalog overrides them; the language matcher is rebuilt so subsequent
GetPreferredLanguage calls can resolve the new language.
*/
func AddOrEditLanguage(lang language.Tag, locales map[int]string) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := catalog[lang]; !exists {
		catalog[lang] = make(map[int]string)
		languages = append(languages, lang)
	}

	maps.Copy(catalog[lang], locales)
	matcher = language.NewMatcher(languages)
}

/*
GetPreferredLanguage returns the language to use for the given request. The
"lang" cookie wins over the "Accept-Language" header (the cookie is an
explicit preference; the header is a hint). Falls back to English on a nil
request or when no supported language is matched.
*/
func GetPreferredLanguage(req *http.Request) language.Tag {
	mu.RLock()
	m := matcher
	mu.RUnlock()

	var cookieValue string
	var header string
	if req != nil {
		if cookie, err := req.Cookie("lang"); err == nil {
			cookieValue = cookie.Value
		}

		header = req.Header.Get("Accept-Language")
	}

	tag, _ := language.MatchStrings(m, cookieValue, header)
	return tag
}

/*
Message returns the localized message for the given (request, status) pair.
The language is resolved from the request via GetPreferredLanguage. Returns
the English fallback when the resolved language has no entry for status, and
an empty string when even English has no entry — the caller is responsible
for handling that (typically by passing the status through HTTPStatusToCode
to keep the wire shape valid).
*/
func Message(req *http.Request, status int) string {
	lang := GetPreferredLanguage(req)

	mu.RLock()
	defer mu.RUnlock()

	if msg, ok := catalog[lang][status]; ok && msg != "" {
		return msg
	}

	return catalog[language.English][status]
}
