package event

import (
	"errors"
	"slices"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// maxBaggageBytes is the total budget a W3C `baggage` header is allowed to use,
// as defined by https://www.w3.org/TR/baggage/. Propagated refuses to build a set
// of members exceeding it rather than letting the header be silently dropped by
// an intermediary.
const maxBaggageBytes = 8192

// errBaggageTooLarge is returned by Propagated when the allow-listed members do
// not fit the W3C baggage budget.
var errBaggageTooLarge = errors.New("event: propagated baggage exceeds the 8192-byte W3C budget")

// AppendAttrs appends the event's span attributes to dst and returns the extended
// slice, in the manner of the standard library's append. It is non-reflective, and
// beyond the growth of dst it allocates only for the keys it has to build — the
// Meta and Subscriptions entries — and the timestamp, so it is safe on the hot
// path.
//
// Attributes are the local, in-process view of an Event: they stay inside the
// trace and are never sent to another party. The full Event is therefore
// represented here, including the personal data that Propagated withholds.
//
// Stamp these on the entry span of a trace — the HTTP handler, the GraphQL
// resolver, the workflow — rather than on every child span. Callers should gate
// on span.IsRecording to avoid the work when nothing is sampling.
//
// Keys follow the OpenTelemetry semantic conventions where one exists
// (enduser.id, user_agent.original, client.address) and fall back to the
// "event.*" namespace otherwise.
func (e Event) AppendAttrs(dst []attribute.KeyValue) []attribute.KeyValue {

	// Grow once rather than letting append double its way up from a nil slice,
	// which is how every caller starts: an event with the fields a web request
	// carries costs five or six reallocations and copies otherwise, on the entry
	// span of every request. The hint covers the identity and application fields
	// that are commonly set, plus the two collections, which are the only parts
	// whose size varies. An event carrying more grows normally from a larger base.
	dst = slices.Grow(dst, 16+len(e.Meta)+len(e.Subscriptions)*7)

	dst = appendStr(dst, "event.id", e.ID)
	dst = appendStr(dst, "event.name", e.Name)

	// Semantic conventions, so that backends light up their built-in views.
	dst = appendStr(dst, string(semconv.EnduserIDKey), e.UserID)
	dst = appendStr(dst, string(semconv.UserAgentOriginalKey), e.UserAgent)
	dst = appendStr(dst, string(semconv.ClientAddressKey), e.IP)

	dst = appendStr(dst, "event.organization_id", e.OrganizationID)
	dst = appendStr(dst, "event.tenant_id", e.TenantID)
	dst = appendStr(dst, "event.locale", e.Locale)
	dst = appendStr(dst, "event.timezone", e.Timezone)

	if e.IsAnonymous != nil {
		dst = append(dst, attribute.Bool("event.is_anonymous", *e.IsAnonymous))
	}
	if !e.Timestamp.IsZero() {
		dst = append(dst, attribute.String("event.timestamp", e.Timestamp.Format(time.RFC3339Nano)))
	}

	for k, v := range e.Meta {
		dst = appendStr(dst, "event.meta."+k, v)
	}

	dst = appendStr(dst, "event.app.name", e.App.Name)
	dst = appendStr(dst, "event.app.version", e.App.Version)
	dst = appendStr(dst, "event.app.build_id", e.App.BuildID)

	dst = appendStr(dst, "event.campaign.name", e.Campaign.Name)
	dst = appendStr(dst, "event.campaign.source", e.Campaign.Source)
	dst = appendStr(dst, "event.campaign.medium", e.Campaign.Medium)
	dst = appendStr(dst, "event.campaign.term", e.Campaign.Term)
	dst = appendStr(dst, "event.campaign.content", e.Campaign.Content)

	dst = appendStr(dst, "event.device.id", e.Device.ID)
	dst = appendStr(dst, "event.device.manufacturer", e.Device.Manufacturer)
	dst = appendStr(dst, "event.device.model", e.Device.Model)
	dst = appendStr(dst, "event.device.name", e.Device.Name)
	dst = appendStr(dst, "event.device.type", e.Device.Type)
	dst = appendStr(dst, "event.device.version", e.Device.Version)
	dst = appendStr(dst, "event.device.advertising_id", e.Device.AdvertisingID)

	dst = appendStr(dst, "event.location.city", e.Location.City)
	dst = appendStr(dst, "event.location.country", e.Location.Country)
	dst = appendStr(dst, "event.location.region", e.Location.Region)
	dst = appendFloat(dst, "event.location.latitude", e.Location.Latitude)
	dst = appendFloat(dst, "event.location.longitude", e.Location.Longitude)
	dst = appendFloat(dst, "event.location.speed", e.Location.Speed)

	dst = appendBool(dst, "event.network.bluetooth", e.Network.Bluetooth)
	dst = appendBool(dst, "event.network.cellular", e.Network.Cellular)
	dst = appendBool(dst, "event.network.wifi", e.Network.WIFI)
	dst = appendStr(dst, "event.network.carrier", e.Network.Carrier)

	dst = appendStr(dst, "event.os.name", e.OS.Name)
	dst = appendStr(dst, "event.os.arch", e.OS.Arch)
	dst = appendStr(dst, "event.os.version", e.OS.Version)

	dst = appendStr(dst, "event.page.path", e.Page.Path)
	dst = appendStr(dst, "event.page.referrer", e.Page.Referrer)
	dst = appendStr(dst, "event.page.search", e.Page.Search)
	dst = appendStr(dst, "event.page.title", e.Page.Title)
	dst = appendStr(dst, "event.page.url", e.Page.URL)

	dst = appendStr(dst, "event.referrer.type", e.Referrer.Type)
	dst = appendStr(dst, "event.referrer.name", e.Referrer.Name)
	dst = appendStr(dst, "event.referrer.url", e.Referrer.URL)
	dst = appendStr(dst, "event.referrer.link", e.Referrer.Link)

	dst = appendInt(dst, "event.screen.density", e.Screen.Density)
	dst = appendInt(dst, "event.screen.width", e.Screen.Width)
	dst = appendInt(dst, "event.screen.height", e.Screen.Height)

	for i, s := range e.Subscriptions {
		prefix := "event.subscriptions." + strconv.Itoa(i) + "."
		dst = appendStr(dst, prefix+"id", s.ID)
		dst = appendStr(dst, prefix+"tenant_id", s.TenantID)
		dst = appendStr(dst, prefix+"customer_id", s.CustomerID)
		dst = appendStr(dst, prefix+"product_id", s.ProductID)
		dst = appendStr(dst, prefix+"price_id", s.PriceID)
		dst = appendStr(dst, prefix+"usage", s.Usage)
		dst = appendFloat(dst, prefix+"increment_by", s.IncrementBy)

		for k, v := range s.Metadata {
			dst = appendStr(dst, prefix+"metadata."+k, v)
		}
	}

	return dst
}

// propagatedFields is the allow-list of Event fields that cross a process
// boundary, each bound to the baggage key it travels under.
//
// It is the one list both directions read: Propagated writes these members and
// eventFromBaggage reads exactly these back. Keeping them in one place is what
// stops the inbound side from accepting a field the outbound side would never
// send — which is how an internet client could otherwise put a user identifier,
// an IP or thousands of subscriptions into an Event by setting a header.
var propagatedFields = [...]struct {
	key   string
	field func(*Event) *string
}{
	{"event.id", func(e *Event) *string { return &e.ID }},
	{"event.name", func(e *Event) *string { return &e.Name }},
	{"event.organization_id", func(e *Event) *string { return &e.OrganizationID }},
	{"event.tenant_id", func(e *Event) *string { return &e.TenantID }},
	{"event.app.name", func(e *Event) *string { return &e.App.Name }},
	{"event.app.version", func(e *Event) *string { return &e.App.Version }},
}

// Propagated returns the allow-listed subset of the Event carried across process
// boundaries as W3C baggage members.
//
// Baggage rides outbound request headers, which means it reaches every endpoint
// the process calls — including third parties. Only opaque, low-cardinality,
// non-personal identifiers are therefore propagated: the event and application
// identity, and the tenancy identifiers needed to correlate one logical request
// across services.
//
// Personal data is never propagated. UserID, IP, UserAgent, and the device,
// location, network, page, screen, campaign and referrer details stay on the
// local span, where AppendAttrs puts them.
//
// Values are taken as they are: any UTF-8 string is accepted, and the propagator
// percent-encodes it on the wire. Propagated reports an error when the encoded
// members exceed the 8192-byte W3C budget rather than truncating silently,
// because a header an intermediary drops is worse than one the caller knows was
// refused.
func (e Event) Propagated() ([]baggage.Member, error) {
	var (
		members []baggage.Member
		size    int
	)

	for _, f := range propagatedFields {
		value := *f.field(&e)
		if value == "" {
			continue
		}

		m, err := baggage.NewMemberRaw(f.key, value)
		if err != nil {
			return nil, err
		}

		// Account for the member as it is serialized on the wire — percent-encoded —
		// plus the "," joining it to the previous one.
		size += len(m.String())
		if len(members) > 0 {
			size++
		}

		if size > maxBaggageBytes {
			return nil, errBaggageTooLarge
		}

		members = append(members, m)
	}

	return members, nil
}

// appendStr appends a string attribute when the value is non-empty. Empty values
// are omitted so that absent fields do not become empty attributes.
func appendStr(dst []attribute.KeyValue, key, value string) []attribute.KeyValue {
	if value == "" {
		return dst
	}

	return append(dst, attribute.String(key, value))
}

// appendBool appends a bool attribute when the value is true. False is the zero
// value and is omitted, the same way an unset string is: an attribute saying a
// capability is absent is indistinguishable from one that was never reported.
func appendBool(dst []attribute.KeyValue, key string, value bool) []attribute.KeyValue {
	if !value {
		return dst
	}

	return append(dst, attribute.Bool(key, value))
}

// appendInt appends an int64 attribute when the value is non-zero.
func appendInt(dst []attribute.KeyValue, key string, value int64) []attribute.KeyValue {
	if value == 0 {
		return dst
	}

	return append(dst, attribute.Int64(key, value))
}

// appendFloat appends a float64 attribute when the value is non-zero.
func appendFloat(dst []attribute.KeyValue, key string, value float64) []attribute.KeyValue {
	if value == 0 {
		return dst
	}

	return append(dst, attribute.Float64(key, value))
}
