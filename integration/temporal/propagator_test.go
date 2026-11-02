package temporal

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/event"
	internallog "github.com/mountayaapp/helix.go/internal/telemetry/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/baggage"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type testHeaderStore struct {
	fields map[string]*commonpb.Payload
}

func newTestHeaderStore() *testHeaderStore {
	return &testHeaderStore{
		fields: make(map[string]*commonpb.Payload),
	}
}

func (h *testHeaderStore) Set(key string, value *commonpb.Payload) {
	h.fields[key] = value
}

func (h *testHeaderStore) Get(key string) (*commonpb.Payload, bool) {
	v, ok := h.fields[key]
	return v, ok
}

func (h *testHeaderStore) ForEachKey(handler func(string, *commonpb.Payload) error) error {
	for k, v := range h.fields {
		if err := handler(k, v); err != nil {
			return err
		}
	}

	return nil
}

var _ workflow.HeaderWriter = (*testHeaderStore)(nil)
var _ workflow.HeaderReader = (*testHeaderStore)(nil)

// fullEvent is an Event with every kind of field set, personal data included.
func fullEvent() event.Event {
	return event.Event{
		ID:             "evt_1",
		Name:           "order.placed",
		UserID:         "usr_1",
		OrganizationID: "org_1",
		TenantID:       "tnt_1",
		IP:             "203.0.113.7",
		UserAgent:      "Example/1.0",
		Locale:         "en-US",
		Timezone:       "UTC",
		Timestamp:      time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC),
		IsAnonymous:    event.BoolPtr(false),
		Meta:           map[string]string{"source": "api"},
		App:            event.App{Name: "storefront", Version: "2.0.0", BuildID: "b42"},
		Device:         event.Device{ID: "dev_1", Type: "mobile"},
		Location:       event.Location{Country: "FR", City: "Grenoble"},
		Subscriptions:  []event.Subscription{{ID: "sub_1", CustomerID: "cus_1"}},
	}
}

// newTestPropagator returns a propagator configured as cfg would configure it.
func newTestPropagator(cfg ConfigClient) *customPropagator {
	return newCustomPropagator(&cfg, internallog.NewNopLogger())
}

// decodeHeader decodes the Event header in h with dc.
func decodeHeader(t *testing.T, h *testHeaderStore, dc converter.DataConverter) event.Event {
	t.Helper()

	payload, ok := h.Get(event.Key)
	require.True(t, ok, "the event header was not written")

	var e event.Event
	require.NoError(t, dc.FromPayload(payload, &e))

	return e
}

func TestPropagator_Inject_SendsOnlyAllowListedFieldsByDefault(t *testing.T) {
	ctx := event.NewContext(t.Context(), fullEvent())

	headers := newTestHeaderStore()
	require.NoError(t, newTestPropagator(ConfigClient{}).Inject(ctx, headers))

	got := decodeHeader(t, headers, converter.GetDefaultDataConverter())

	// The six identifiers Propagated sends as baggage, and nothing else: no user
	// identifier, address, user agent, device, location, meta or subscription
	// reaches workflow history.
	assert.Equal(t, event.Event{
		ID:             "evt_1",
		Name:           "order.placed",
		OrganizationID: "org_1",
		TenantID:       "tnt_1",
		App:            event.App{Name: "storefront", Version: "2.0.0"},
	}, got)
}

func TestPropagator_Inject_SendsTheFullEventWhenOptedIn(t *testing.T) {
	input := fullEvent()
	ctx := event.NewContext(t.Context(), input)

	headers := newTestHeaderStore()
	require.NoError(t, newTestPropagator(ConfigClient{PropagateFullEvent: true}).Inject(ctx, headers))

	got := decodeHeader(t, headers, converter.GetDefaultDataConverter())
	assert.True(t, input.Timestamp.Equal(got.Timestamp))

	got.Timestamp = input.Timestamp
	assert.Equal(t, input, got)
}

// markingCodec is a payload codec that wraps every payload in an encoding of its
// own, standing in for an encrypting codec: a payload it did not encode cannot be
// read without it.
type markingCodec struct{}

const markingEncoding = "binary/test-marked"

func (markingCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		data, err := p.Marshal()
		if err != nil {
			return nil, err
		}

		out[i] = &commonpb.Payload{
			Metadata: map[string][]byte{converter.MetadataEncoding: []byte(markingEncoding)},
			Data:     data,
		}
	}

	return out, nil
}

func (markingCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.GetMetadata()[converter.MetadataEncoding]) != markingEncoding {
			out[i] = p
			continue
		}

		decoded := &commonpb.Payload{}
		if err := decoded.Unmarshal(p.GetData()); err != nil {
			return nil, err
		}

		out[i] = decoded
	}

	return out, nil
}

func TestPropagator_Inject_EncodesWithTheConfiguredDataConverter(t *testing.T) {
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), markingCodec{})
	ctx := event.NewContext(t.Context(), fullEvent())

	headers := newTestHeaderStore()
	require.NoError(t, newTestPropagator(ConfigClient{DataConverter: dc}).Inject(ctx, headers))

	// The codec ran on the header, as it does on arguments: what the server stores
	// is the codec's encoding, not plain JSON.
	payload, ok := headers.Get(event.Key)
	require.True(t, ok)
	assert.Equal(t, markingEncoding, string(payload.GetMetadata()[converter.MetadataEncoding]))

	var plain event.Event
	assert.Error(t, converter.GetDefaultDataConverter().FromPayload(payload, &plain),
		"the header must not be readable without the configured codec")

	assert.Equal(t, "evt_1", decodeHeader(t, headers, dc).ID)
}

func TestPropagator_Inject_IgnoresAnEventRebuiltFromBaggage(t *testing.T) {
	// Baggage as an inbound request would carry it: event.FromContext rebuilds an
	// Event from it, but nothing set one explicitly.
	member, err := baggage.NewMemberRaw("event.name", "spoofed")
	require.NoError(t, err)

	bag, err := baggage.New(member)
	require.NoError(t, err)

	ctx := baggage.ContextWithBaggage(t.Context(), bag)

	_, found := event.FromContext(ctx)
	require.True(t, found, "precondition: event.FromContext rebuilds an Event from baggage")

	headers := newTestHeaderStore()
	require.NoError(t, newTestPropagator(ConfigClient{}).Inject(ctx, headers))

	_, ok := headers.Get(event.Key)
	assert.False(t, ok, "an Event rebuilt from baggage must not be written to the header")
}

func TestPropagator_Inject_ExplicitEventWinsOverBaggage(t *testing.T) {
	member, err := baggage.NewMemberRaw("event.name", "from-baggage")
	require.NoError(t, err)

	bag, err := baggage.New(member)
	require.NoError(t, err)

	ctx := baggage.ContextWithBaggage(t.Context(), bag)
	ctx = event.NewContext(ctx, event.Event{Name: "explicit"})

	headers := newTestHeaderStore()
	require.NoError(t, newTestPropagator(ConfigClient{}).Inject(ctx, headers))

	assert.Equal(t, "explicit", decodeHeader(t, headers, converter.GetDefaultDataConverter()).Name)
}

func TestPropagator_Inject_WritesNothingWithoutAnEvent(t *testing.T) {
	headers := newTestHeaderStore()
	require.NoError(t, newTestPropagator(ConfigClient{}).Inject(t.Context(), headers))

	assert.Empty(t, headers.fields)
}

func TestPropagator_Inject_WritesNothingWhenNoAllowListedFieldIsSet(t *testing.T) {
	// Only personal data: none of it is propagated by default, so there is
	// nothing to send, and no empty header is written either.
	ctx := event.NewContext(t.Context(), event.Event{UserID: "usr_1", IP: "203.0.113.7"})

	headers := newTestHeaderStore()
	require.NoError(t, newTestPropagator(ConfigClient{}).Inject(ctx, headers))

	assert.Empty(t, headers.fields)
}

func TestPropagator_Extract(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	p := newTestPropagator(ConfigClient{})

	payloadOf := func(t *testing.T, v any) *commonpb.Payload {
		t.Helper()

		payload, err := dc.ToPayload(v)
		require.NoError(t, err)

		return payload
	}

	t.Run("a header carrying an Event is extracted", func(t *testing.T) {
		headers := newTestHeaderStore()
		headers.Set(event.Key, payloadOf(t, event.Event{ID: "evt_1", Name: "order.placed"}))

		ctx, err := p.Extract(t.Context(), headers)
		require.NoError(t, err)

		e, ok := EventFromActivity(ctx)
		assert.True(t, ok)
		assert.Equal(t, "evt_1", e.ID)

		// The integrations an activity calls read it through event.FromContext.
		e, ok = event.FromContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, "order.placed", e.Name)
	})

	t.Run("no header reports no Event", func(t *testing.T) {
		ctx, err := p.Extract(t.Context(), newTestHeaderStore())
		require.NoError(t, err)

		_, ok := EventFromActivity(ctx)
		assert.False(t, ok)
	})

	t.Run("an empty Event, as earlier versions sent, reports no Event", func(t *testing.T) {
		headers := newTestHeaderStore()
		headers.Set(event.Key, payloadOf(t, event.Event{}))

		ctx, err := p.Extract(t.Context(), headers)
		require.NoError(t, err)

		_, ok := EventFromActivity(ctx)
		assert.False(t, ok)
	})

	t.Run("a header that cannot be decoded reports no Event and no error", func(t *testing.T) {
		headers := newTestHeaderStore()
		headers.Set(event.Key, payloadOf(t, "not an event"))

		ctx, err := p.Extract(t.Context(), headers)
		require.NoError(t, err)

		_, ok := EventFromActivity(ctx)
		assert.False(t, ok)
	})

	t.Run("a context that is not an activity's is not a problem", func(t *testing.T) {
		// The SDK also extracts on the client side, when a query is answered: the
		// context there is not an activity context.
		headers := newTestHeaderStore()
		headers.Set(event.Key, payloadOf(t, event.Event{Name: "order.placed"}))

		assert.NotPanics(t, func() {
			_, err := p.Extract(context.Background(), headers)
			assert.NoError(t, err)
		})
	})
}

func TestAllowListed_MatchesPropagated(t *testing.T) {
	e := fullEvent()

	// What the header sends by default is what Propagated sends as baggage.
	want, err := e.Propagated()
	require.NoError(t, err)

	got, err := allowListed(e).Propagated()
	require.NoError(t, err)

	keys := func(members []baggage.Member) []string {
		out := make([]string, 0, len(members))
		for _, m := range members {
			out = append(out, m.Key()+"="+m.Value())
		}

		slices.Sort(out)
		return out
	}

	assert.Equal(t, keys(want), keys(got))

	// And nothing beyond it: rebuilding an Event from those members — which reads
	// exactly the allow-list — gives back allowListed's result.
	bag, err := baggage.New(want...)
	require.NoError(t, err)

	rebuilt, ok := event.FromContext(baggage.ContextWithBaggage(t.Context(), bag))
	require.True(t, ok)
	assert.Equal(t, rebuilt, allowListed(e))
}

// propagationResult is what the workflow and activity in
// TestPropagator_ThroughAWorkflow saw.
type propagationResult struct {
	WorkflowEvent    event.Event
	WorkflowHasEvent bool
	ActivityEvent    event.Event
	ActivityHasEvent bool
}

// runPropagation runs a workflow that reads its Event and passes it on to an
// activity, under the SDK's test environment, with header as the workflow's
// header.
func runPropagation(t *testing.T, header *commonpb.Header) propagationResult {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{newTestPropagator(ConfigClient{})})
	if header != nil {
		env.SetHeader(header)
	}

	readEvent := NewActivity[NoInput, propagationResult]("read-event", workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	env.RegisterActivityWithOptions(func(ctx context.Context, _ NoInput) (propagationResult, error) {
		e, ok := EventFromActivity(ctx)
		return propagationResult{ActivityEvent: e, ActivityHasEvent: ok}, nil
	}, activity.RegisterOptions{Name: readEvent.Name()})

	env.ExecuteWorkflow(func(ctx workflow.Context) (propagationResult, error) {
		e, ok := EventFromWorkflow(ctx)

		result, err := readEvent.Execute(ctx, NoInput{}).Await(ctx)
		if err != nil {
			return propagationResult{}, err
		}

		result.WorkflowEvent, result.WorkflowHasEvent = e, ok
		return result, nil
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result propagationResult
	require.NoError(t, env.GetWorkflowResult(&result))

	return result
}

func TestPropagator_ThroughAWorkflow(t *testing.T) {
	t.Run("the Event follows the workflow into its activity", func(t *testing.T) {
		payload, err := converter.GetDefaultDataConverter().ToPayload(event.Event{ID: "evt_1", Name: "order.placed"})
		require.NoError(t, err)

		result := runPropagation(t, &commonpb.Header{Fields: map[string]*commonpb.Payload{event.Key: payload}})

		assert.True(t, result.WorkflowHasEvent)
		assert.Equal(t, "evt_1", result.WorkflowEvent.ID)
		assert.True(t, result.ActivityHasEvent)
		assert.Equal(t, "order.placed", result.ActivityEvent.Name)
	})

	t.Run("a workflow started without an Event reports none, and sends none on", func(t *testing.T) {
		result := runPropagation(t, nil)

		assert.False(t, result.WorkflowHasEvent)
		assert.False(t, result.ActivityHasEvent)
	})

	t.Run("a header that cannot be decoded does not fail the workflow", func(t *testing.T) {
		payload, err := converter.GetDefaultDataConverter().ToPayload([]int{1, 2, 3})
		require.NoError(t, err)

		result := runPropagation(t, &commonpb.Header{Fields: map[string]*commonpb.Payload{event.Key: payload}})

		assert.False(t, result.WorkflowHasEvent)
		assert.False(t, result.ActivityHasEvent)
	})
}

func TestPropagator_Inject_ReportsAnEncodingFailure(t *testing.T) {
	p := newTestPropagator(ConfigClient{DataConverter: failingConverter{}})
	ctx := event.NewContext(t.Context(), event.Event{Name: "order.placed"})

	err := p.Inject(ctx, newTestHeaderStore())
	assert.ErrorIs(t, err, errFailingConverter)
}

// errFailingConverter is what failingConverter returns.
var errFailingConverter = errors.New("converter failed")

// failingConverter is a DataConverter that cannot encode anything.
type failingConverter struct {
	converter.DataConverter
}

func (failingConverter) ToPayload(any) (*commonpb.Payload, error) {
	return nil, errFailingConverter
}
