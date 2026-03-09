package graphql

import (
	"maps"
	"net/http"

	"golang.org/x/text/language"
)

/*
supportedLanguages is a list of supported languages to handle default error
messages in the HTTP API.

English is the default language.
*/
var supportedLanguages = []language.Tag{
	language.English,
}

/*
supportedMatcher is a pre-built language matcher, rebuilt only when
AddOrEditLanguage is called (at init time, before serving).
*/
var supportedMatcher = language.NewMatcher(supportedLanguages)

/*
supportedLocales represents the locales handled by each language for the given
status code.
*/
var supportedLocales = map[language.Tag]map[int]string{
	language.English: {
		http.StatusBadRequest:            "Failed to validate request",
		http.StatusUnauthorized:          "You are not authorized to perform this action",
		http.StatusPaymentRequired:       "Request failed because payment is required",
		http.StatusForbidden:             "You don't have required permissions to perform this action",
		http.StatusNotFound:              "Resource does not exist",
		http.StatusMethodNotAllowed:      "Resource does not support this method",
		http.StatusConflict:              "Failed to process target resource because of conflict",
		http.StatusRequestEntityTooLarge: "Can not process payload too large",
		http.StatusTooManyRequests:       "Request-rate limit has been reached",
		http.StatusInternalServerError:   "We have been notified of this unexpected internal error",
		http.StatusServiceUnavailable:    "Please try again in a few moments",
	},
}

/*
AddOrEditLanguage allows a service to add or edit a language support for error
messages in the GraphQL API, based on the status code returned.

Supported status code:

  - [http.StatusBadRequest]
  - [http.StatusUnauthorized]
  - [http.StatusPaymentRequired]
  - [http.StatusForbidden]
  - [http.StatusNotFound]
  - [http.StatusMethodNotAllowed]
  - [http.StatusConflict]
  - [http.StatusRequestEntityTooLarge]
  - [http.StatusTooManyRequests]
  - [http.StatusInternalServerError]
  - [http.StatusServiceUnavailable]

Example:

	graphql.AddOrEditLanguage(language.French, map[int]string{
		http.StatusBadRequest:            "<locale>",
		http.StatusUnauthorized:          "<locale>",
		http.StatusPaymentRequired:       "<locale>",
		http.StatusForbidden:             "<locale>",
		http.StatusNotFound:              "<locale>",
		http.StatusMethodNotAllowed:      "<locale>",
		http.StatusConflict:              "<locale>",
		http.StatusRequestEntityTooLarge: "<locale>",
		http.StatusTooManyRequests:       "<locale>",
		http.StatusInternalServerError:   "<locale>",
		http.StatusServiceUnavailable:    "<locale>",
	})
*/
func AddOrEditLanguage(lang language.Tag, locales map[int]string) {
	if _, exists := supportedLocales[lang]; !exists {
		supportedLocales[lang] = make(map[int]string)
		supportedLanguages = append(supportedLanguages, lang)
	}

	maps.Copy(supportedLocales[lang], locales)
	supportedMatcher = language.NewMatcher(supportedLanguages)
}

/*
getPreferredLanguage returns the preferred language requested by the client. It
relies on the cookie, then on the "Accept-Language" header (order matters).
*/
func getPreferredLanguage(req *http.Request) language.Tag {
	var cookieValue string
	var header string
	if req != nil {
		if cookie, err := req.Cookie("lang"); err == nil {
			cookieValue = cookie.Value
		}

		header = req.Header.Get("Accept-Language")
	}

	tag, _ := language.MatchStrings(supportedMatcher, cookieValue, header)
	return tag
}
