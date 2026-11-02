// Package errorstack exposes a minimal, GraphQL-spec-compliant error type used
// across the helix.go ecosystem.
//
// Errors serialize to the standard GraphQL response envelope:
//
//	{
//	  "errors": [
//	    {
//	      "message": "Must be a valid email address",
//	      "path": ["request", "body", "email"],
//	      "extensions": {"code": "VALIDATION_FAILED"}
//	    }
//	  ],
//	  "extensions": {…}
//	}
//
// Use New to build a single-entry error (extensions.code defaults to
// CodeInternalError, override with WithCode). Use NewValidation to build a
// multi-entry error where every entry carries CodeValidationFailed. Use Wrap to
// preserve an existing error in the chain for errors.Is/As support.
//
// Use Nest — not Wrap — when aggregating the failures of several components into
// one error. Wrap keeps its cause unserialized, so the entries of the error it
// wraps do not survive into the envelope; Nest carries them through and records
// which component they came from in their path.
//
// This package must not import any other package of this ecosystem.
package errorstack
