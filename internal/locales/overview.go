// Package locales centralizes the canonical HTTP-status → message catalog used by
// the REST, GraphQL and MCP integrations and by integration.WriteError. The
// transports are thin wrappers around the same backing state, so a service that
// adds or edits a locale through any of their AddOrEditLanguage functions sees the
// change applied across every error path — no silent divergence between them.
package locales
