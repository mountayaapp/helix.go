// Package documents ships the OpenAPI documents that describe what helix writes on
// the wire: the success and error envelopes of the REST integration, their
// response components, and the headers they carry.
//
// A description references them by their raw URL at a tag:
//
//	$ref: https://raw.githubusercontent.com/mountaya/helix.go/refs/tags/<tag>/documents/openapi/components/responses/500.yaml
//
// which is what a reader of the description, a bundler or a documentation site
// resolves. A running server does not: the REST integration reads them from
// OpenAPI, the copy embedded in the module it is built with, so it starts without
// network access and validates its responses against the envelope its own helix
// writes.
package documents

import "embed"

// OpenAPI holds the openapi directory of this package. A reference's path after
// "documents/" is its path in OpenAPI: the document a description names as
// .../documents/openapi/components/responses/500.yaml is
// openapi/components/responses/500.yaml here.
//
//go:embed openapi
var OpenAPI embed.FS
