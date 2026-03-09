package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/event"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
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

func newNoopSpan(ctx context.Context) (context.Context, trace.Span) {
	tracer := noop.NewTracerProvider().Tracer("test")
	return tracer.Start(ctx, "test-span")
}

func TestPropagator_Inject_WritesEventToHeader(t *testing.T) {
	e := event.Event{
		Name:   "subscribed",
		UserID: "user_123",
	}

	ctx := event.ContextWithEvent(t.Context(), e)
	_, span := newNoopSpan(ctx)
	ctx = context.WithValue(ctx, spanCtxKey, span)

	headers := newTestHeaderStore()
	p := &custompropagator{cachedCtx: context.Background()}
	err := p.Inject(ctx, headers)

	assert.NoError(t, err)

	payload, ok := headers.Get(event.Key)
	assert.True(t, ok)
	assert.NotNil(t, payload)
}

func TestPropagator_Inject_NoEventInContext(t *testing.T) {
	headers := newTestHeaderStore()
	p := &custompropagator{cachedCtx: context.Background()}
	err := p.Inject(t.Context(), headers)

	assert.NoError(t, err)

	_, ok := headers.Get(event.Key)
	assert.False(t, ok)
}

func TestPropagator_Inject_EventDeserializesCorrectly(t *testing.T) {
	input := event.Event{
		Name:   "subscribed",
		UserID: "user_123",
		Meta: map[string]string{
			"source": "api",
		},
	}

	ctx := event.ContextWithEvent(t.Context(), input)
	_, span := newNoopSpan(ctx)
	ctx = context.WithValue(ctx, spanCtxKey, span)

	headers := newTestHeaderStore()
	p := &custompropagator{cachedCtx: context.Background()}
	err := p.Inject(ctx, headers)
	assert.NoError(t, err)

	payload, ok := headers.Get(event.Key)
	assert.True(t, ok)

	var output event.Event
	err = converter.GetDefaultDataConverter().FromPayload(payload, &output)
	assert.NoError(t, err)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
	assert.Equal(t, input.Meta, output.Meta)
}

func TestPropagator_InjectDeserialize_RoundTrip(t *testing.T) {
	input := event.Event{
		Name:   "checkout",
		UserID: "user_round_trip",
		Meta: map[string]string{
			"env":    "test",
			"region": "us-east-1",
		},
		App: event.App{Name: "helix", Version: "1.0.0"},
		Subscriptions: []event.Subscription{
			{ID: "sub_001", CustomerID: "cus_001", ProductID: "prod_001"},
		},
	}

	ctx := event.ContextWithEvent(t.Context(), input)
	_, span := newNoopSpan(ctx)
	ctx = context.WithValue(ctx, spanCtxKey, span)

	headers := newTestHeaderStore()
	p := &custompropagator{cachedCtx: context.Background()}
	err := p.Inject(ctx, headers)
	assert.NoError(t, err)

	payload, ok := headers.Get(event.Key)
	assert.True(t, ok)

	var output event.Event
	err = converter.GetDefaultDataConverter().FromPayload(payload, &output)
	assert.NoError(t, err)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
	assert.Equal(t, input.Meta, output.Meta)
	assert.Equal(t, input.App, output.App)
	assert.Len(t, output.Subscriptions, 1)
	assert.Equal(t, "sub_001", output.Subscriptions[0].ID)
	assert.Equal(t, "cus_001", output.Subscriptions[0].CustomerID)
	assert.Equal(t, "prod_001", output.Subscriptions[0].ProductID)
}

func TestPropagator_InjectDeserialize_FullEvent(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	input := event.Event{
		ID:             "evt_temporal",
		Name:           "workflow_started",
		UserID:         "user_temporal",
		OrganizationID: "org_temporal",
		TenantID:       "tenant_temporal",
		IP:             "10.0.0.1",
		UserAgent:      "TemporalWorker/1.0",
		Locale:         "en-US",
		Timezone:       "UTC",
		Timestamp:      ts,
		IsAnonymous:    event.BoolPtr(true),
		Meta:           map[string]string{"workflow": "process_order"},
		App:            event.App{Name: "worker", Version: "2.0.0"},
		Campaign:       event.Campaign{Name: "launch", Source: "internal"},
		Device:         event.Device{ID: "srv_1", Type: "server"},
		Location:       event.Location{Country: "US", Region: "us-east-1"},
		OS:             event.OS{Name: "Linux", Arch: "amd64"},
		Page:           event.Page{Path: "/api/orders"},
		Screen:         event.Screen{Density: 1, Width: 1920, Height: 1080},
		Subscriptions: []event.Subscription{
			{ID: "sub_a", CustomerID: "cus_a", ProductID: "prod_a", PriceID: "price_a"},
			{ID: "sub_b", CustomerID: "cus_b", ProductID: "prod_b", PriceID: "price_b"},
		},
	}

	ctx := event.ContextWithEvent(t.Context(), input)
	_, span := newNoopSpan(ctx)
	ctx = context.WithValue(ctx, spanCtxKey, span)

	headers := newTestHeaderStore()
	p := &custompropagator{cachedCtx: context.Background()}
	err := p.Inject(ctx, headers)
	assert.NoError(t, err)

	payload, ok := headers.Get(event.Key)
	assert.True(t, ok)

	var output event.Event
	err = converter.GetDefaultDataConverter().FromPayload(payload, &output)
	assert.NoError(t, err)
	assert.Equal(t, input.ID, output.ID)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
	assert.Equal(t, input.OrganizationID, output.OrganizationID)
	assert.Equal(t, input.TenantID, output.TenantID)
	assert.Equal(t, input.IP, output.IP)
	assert.Equal(t, input.UserAgent, output.UserAgent)
	assert.Equal(t, input.Locale, output.Locale)
	assert.Equal(t, input.Timezone, output.Timezone)
	assert.True(t, input.Timestamp.Equal(output.Timestamp))
	assert.NotNil(t, output.IsAnonymous)
	assert.Equal(t, *input.IsAnonymous, *output.IsAnonymous)
	assert.Equal(t, input.Meta, output.Meta)
	assert.Equal(t, input.App, output.App)
	assert.Equal(t, input.Campaign.Name, output.Campaign.Name)
	assert.Equal(t, input.Campaign.Source, output.Campaign.Source)
	assert.Equal(t, input.Device.ID, output.Device.ID)
	assert.Equal(t, input.Device.Type, output.Device.Type)
	assert.Equal(t, input.Location.Country, output.Location.Country)
	assert.Equal(t, input.Location.Region, output.Location.Region)
	assert.Equal(t, input.OS, output.OS)
	assert.Equal(t, input.Page.Path, output.Page.Path)
	assert.Equal(t, input.Screen, output.Screen)
	assert.Len(t, output.Subscriptions, 2)
	assert.Equal(t, "sub_a", output.Subscriptions[0].ID)
	assert.Equal(t, "sub_b", output.Subscriptions[1].ID)
}

func TestPropagator_SerializationRoundTrip_ViaDataConverter(t *testing.T) {
	input := event.Event{
		Name:   "converter_test",
		UserID: "user_conv",
		Meta:   map[string]string{"key": "value"},
		App:    event.App{Name: "test-app", Version: "1.0.0"},
	}

	dc := converter.GetDefaultDataConverter()
	payload, err := dc.ToPayload(input)
	assert.NoError(t, err)
	assert.NotNil(t, payload)

	var output event.Event
	err = dc.FromPayload(payload, &output)
	assert.NoError(t, err)
	assert.Equal(t, input, output)
}

func TestPropagator_Inject_UsesDefaultConverter(t *testing.T) {
	input := event.Event{
		Name: "converter_cached",
	}

	ctx := event.ContextWithEvent(t.Context(), input)

	headers := newTestHeaderStore()
	p := &custompropagator{cachedCtx: context.Background()}
	err := p.Inject(ctx, headers)
	assert.NoError(t, err)

	payload, ok := headers.Get(event.Key)
	assert.True(t, ok)

	// Verify the cached defaultConverter produces compatible payloads.
	var output event.Event
	err = defaultConverter.FromPayload(payload, &output)
	assert.NoError(t, err)
	assert.Equal(t, input.Name, output.Name)
}

func TestPropagator_Inject_NoSpanInContext(t *testing.T) {
	input := event.Event{
		Name:   "no_span_inject",
		UserID: "user_no_span",
	}

	// Context has event but no span — should still inject successfully.
	ctx := event.ContextWithEvent(t.Context(), input)

	headers := newTestHeaderStore()
	p := &custompropagator{cachedCtx: context.Background()}
	err := p.Inject(ctx, headers)
	assert.NoError(t, err)

	payload, ok := headers.Get(event.Key)
	assert.True(t, ok)

	var output event.Event
	err = defaultConverter.FromPayload(payload, &output)
	assert.NoError(t, err)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
}
