// Package temporalrest holds the parts of a Temporal workflow a REST API may
// expose to its clients, in the shape of the "metadata" object of a response.
//
// It is a package of its own so that a REST service can describe a workflow it
// started without importing the Temporal SDK.
package temporalrest
