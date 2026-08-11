package graphql

import (
	"github.com/mountayaapp/helix.go/internal/locales"

	"golang.org/x/text/language"
)

/*
AddOrEditLanguage allows a service to add or edit a language support for error
messages emitted by helix.go transports, based on the status code returned.
The catalog is shared with the REST integration: a single call from either
package surfaces in both error paths so the wire shape stays consistent across
transports.

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
  - [http.StatusNotImplemented]
  - [http.StatusBadGateway]
  - [http.StatusServiceUnavailable]
  - [http.StatusGatewayTimeout]

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
		http.StatusNotImplemented:        "<locale>",
		http.StatusBadGateway:            "<locale>",
		http.StatusServiceUnavailable:    "<locale>",
		http.StatusGatewayTimeout:        "<locale>",
	})
*/
func AddOrEditLanguage(lang language.Tag, m map[int]string) {
	locales.AddOrEditLanguage(lang, m)
}
