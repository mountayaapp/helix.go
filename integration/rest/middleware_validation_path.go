package rest

import (
	"net/url"
	"slices"

	"github.com/mountayaapp/helix.go/errorstack"
)

// undecodablePathMessage is the message of the entry refusing a path parameter
// whose segment does not percent-decode.
const undecodablePathMessage = "Is not a valid percent-encoded path segment"

// decodePathParams returns the path parameters the OpenAPI router matched, each
// percent-decoded, with one entry per parameter whose segment does not decode, at
// ["request", "params", name], in the order of the names.
//
// The router matches the path as the client escaped it, so that an escaped "/"
// stays inside the segment it belongs to, and it hands each parameter on as
// written. The description constrains the value rather than its spelling: a
// simple-style path parameter is the segment percent-decoded, as RFC 3986 and
// OpenAPI define it. Validating the escaped spelling would refuse a value the
// description admits, such as "a b" sent as "a%20b", which a client cannot avoid
// escaping, and would admit one it refuses whenever the escape happens to satisfy
// the schema.
//
// A parameter that does not decode keeps its segment as written, and is refused
// rather than validated: there is no value to hold to the description. The
// router matches against a path that always decodes, so only a template with a
// literal after a parameter in the same segment can produce one: /versions/{name}1
// matches /versions/a%41 with name "a%4", ending the parameter inside an escape.
func decodePathParams(matched map[string]string) (map[string]string, []errorstack.Entry) {
	if len(matched) == 0 {
		return matched, nil
	}

	decoded := make(map[string]string, len(matched))

	var undecodable []string
	for name, segment := range matched {
		value, err := url.PathUnescape(segment)
		if err != nil {
			decoded[name] = segment
			undecodable = append(undecodable, name)
			continue
		}

		decoded[name] = value
	}

	if len(undecodable) == 0 {
		return decoded, nil
	}

	slices.Sort(undecodable)

	entries := make([]errorstack.Entry, 0, len(undecodable))
	for _, name := range undecodable {
		entries = append(entries, validationEntry(undecodablePathMessage, []any{"request", "params", name}))
	}

	return decoded, entries
}
