/*
Package errorstack exposes a minimal, GraphQL-spec-compliant error type used
across the helix.go ecosystem.

Errors serialize to the standard GraphQL response envelope:

	{
	  "errors": [
	    {
	      "message": "Must be a valid email address",
	      "path": ["request", "body", "email"],
	      "extensions": {"code": "VALIDATION_FAILED"}
	    }
	  ],
	  "extensions": {…}
	}

Use New to build a single-entry error (extensions.code defaults to
CodeInternalError, override with WithCode). Use NewValidation to build a
multi-entry error where every entry carries CodeValidationFailed. Use Wrap to
preserve an existing error in the chain for errors.Is/As support.

This package must not import any other package of this ecosystem.
*/
package errorstack
