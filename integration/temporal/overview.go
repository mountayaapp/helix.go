// Package temporal exposes an opinionated way to interact with Temporal, including
// type-safe workflows, activities, signals, queries and updates from clients to
// workers via generics.
//
// Connect registers a client-only connection with a Service as a dependency. New
// registers a worker as a server, and the client it polls with as a dependency.
// Both hand back handles without lifecycle methods: the Service starts, drains and
// closes what they registered.
//
// The Event set on a context with event.NewContext travels with the work, in a
// header of its own that Temporal persists in workflow history. By default it
// carries only the identifiers event.Event.Propagated allows across a process
// boundary; ConfigClient.PropagateFullEvent opts in to the whole Event.
package temporal

// identifier represents the integration's unique identifier.
const identifier = "temporal"

// humanized represents the integration's humanized name.
const humanized = "Temporal"
