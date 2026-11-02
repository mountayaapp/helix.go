package clickhouse

import (
	"context"
	"reflect"
	"sync"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/ClickHouse/clickhouse-go/v2"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// profileEventKeys are the server-side counters worth putting on a span, and the
// attribute each is recorded under.
//
// ClickHouse reports several hundred profile events per query. All but a handful
// describe the server's internals — page cache behaviour, allocator activity —
// and belong in system.query_log rather than on a distributed trace. These four
// answer the question a trace is actually asked: how much data did this touch.
var profileEventKeys = map[string]attribute.Key{
	"SelectedRows":  integration.Key(identifier, "selected.rows"),
	"SelectedBytes": integration.Key(identifier, "selected.bytes"),
	"InsertedRows":  integration.Key(identifier, "inserted.rows"),
	"InsertedBytes": integration.Key(identifier, "inserted.bytes"),
}

// profile accumulates the counters ClickHouse reports while a query runs.
//
// They arrive in batches, on the driver's read goroutine, one batch per reporting
// interval and per server thread, each carrying an increment rather than a total.
// Summing is therefore the only correct reading, and it cannot be done by setting
// an attribute as each batch lands.
type profile struct {
	mu     sync.Mutex
	values map[string]int64
}

// collect sums the events this batch reported, ignoring the ones not on the
// short list.
func (p *profile) collect(events []clickhouse.ProfileEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, event := range events {
		if _, ok := profileEventKeys[event.Name]; !ok {
			continue
		}

		if p.values == nil {
			p.values = make(map[string]int64, len(profileEventKeys))
		}

		p.values[event.Name] += event.Value
	}
}

// attributes returns what was collected, for the span to be stamped with once the
// query has returned. An empty result means the server reported none of the four,
// which is the ordinary case for a statement that reads and writes nothing.
func (p *profile) attributes() []attribute.KeyValue {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.values) == 0 {
		return nil
	}

	attrs := make([]attribute.KeyValue, 0, len(p.values))
	for name, value := range p.values {
		attrs = append(attrs, profileEventKeys[name].Int64(value))
	}

	return attrs
}

// stamp sets what was collected on span, once the statement has returned. It
// builds nothing for a span that is not recording.
func (p *profile) stamp(span *trace.Span) {
	if !span.IsRecording() {
		return
	}

	span.SetAttributes(p.attributes()...)
}

// queryContext returns the context to hand the driver for a traced statement.
//
// It does two things the driver can do and this package cannot. WithSpan ships
// the span's identifiers to the server, which records them in
// system.opentelemetry_span_log — so a slow query can be followed from the trace
// into ClickHouse's own view of what it did. It creates no span: it is
// propagation, and propagation only.
//
// WithProfileEvents asks the server to report its counters as the statement runs,
// which is how a span learns how many rows it actually touched.
//
// Both yield to a caller who set their own through clickhouse.Context: the
// caller's span is shipped instead of the operation's, and the caller's
// profile-events callback is kept, in which case the span carries no counters.
// Overwriting either would silently break what the caller built on it.
func queryContext(ctx context.Context, p *profile, opts ...clickhouse.QueryOption) context.Context {
	opts = append(opts, traced(oteltrace.SpanFromContext(ctx).SpanContext(), p.collect))

	return clickhouse.Context(ctx, opts...)
}

// traced is the QueryOption behind queryContext. It is applied to the options
// already carried by the context, which is the only point at which a caller's
// own can be seen.
//
// The driver keeps the fields of QueryOptions unexported and offers no accessor,
// so whether the caller set a span or a callback is read through reflection. That
// is also why a caller's callback is kept rather than chained: it can be seen to
// be set, but there is no way to call it from outside the driver. Reflection can
// read a field's zero-ness without exporting it, and a driver release that
// renames the fields reads as "not set" — which is to say, as before this check
// existed — rather than as a failure; TestTraced_SeesCallerOptions pins the
// layout so that such a release is noticed.
func traced(span oteltrace.SpanContext, collect func([]clickhouse.ProfileEvent)) clickhouse.QueryOption {
	return func(o *clickhouse.QueryOptions) error {
		if !isSet(o, "span") {
			if err := clickhouse.WithSpan(span)(o); err != nil {
				return err
			}
		}

		if !isSet(o, "events", "profileEvents") {
			return clickhouse.WithProfileEvents(collect)(o)
		}

		return nil
	}
}

// isSet reports whether the field of o found by following path holds anything
// but its zero value. A path that does not resolve reports false.
func isSet(o *clickhouse.QueryOptions, path ...string) bool {
	v := reflect.ValueOf(o).Elem()
	for _, name := range path {
		if v.Kind() != reflect.Struct {
			return false
		}

		v = v.FieldByName(name)
		if !v.IsValid() {
			return false
		}
	}

	return !v.IsZero()
}
