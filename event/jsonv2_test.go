package event

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMarshal_ZeroEventOmitsNumericAndBoolFields pins the wire format across the
// encoding/json v1 -> v2 migration.
//
// v2 redefines omitempty: it omits a field that encodes to an empty JSON value
// ("", [], {}, null), where v1 omitted the Go zero value. A false bool and a 0
// number are NOT empty JSON values, so every numeric and boolean field that used
// omitempty had to move to omitzero to keep emitting nothing. Without that, a
// zero-valued Event would serialize latitude, longitude, speed, bluetooth,
// cellular, wifi, density, width and height on every single event.
func TestMarshal_ZeroEventOmitsNumericAndBoolFields(t *testing.T) {
	b, err := json.Marshal(Event{})
	require.NoError(t, err)

	assert.Equal(t, "{}", string(b))

	for _, field := range []string{
		"latitude", "longitude", "speed",
		"bluetooth", "cellular", "wifi",
		"density", "width", "height",
		"increment_by",
	} {
		assert.NotContains(t, string(b), field,
			"zero-valued %q must be omitted; if this fails the tag is omitempty and needs omitzero", field)
	}
}

func TestMarshal_NonZeroNumericAndBoolFieldsAreEmitted(t *testing.T) {
	e := Event{
		Location: Location{Latitude: 48.85, Longitude: 2.35, Speed: 1.5},
		Network:  Network{Bluetooth: true, Cellular: true, WIFI: true},
		Screen:   Screen{Density: 2, Width: 1920, Height: 1080},
	}

	b, err := json.Marshal(e)
	require.NoError(t, err)

	out := string(b)
	for _, want := range []string{
		`"latitude":48.85`, `"longitude":2.35`, `"speed":1.5`,
		`"bluetooth":true`, `"cellular":true`, `"wifi":true`,
		`"density":2`, `"width":1920`, `"height":1080`,
	} {
		assert.Contains(t, out, want)
	}
}

func TestMarshal_ZeroValuedNumericInsideAPopulatedStructIsStillOmitted(t *testing.T) {
	// Latitude is set but Speed is not: Speed must not appear as "speed":0.
	b, err := json.Marshal(Event{Location: Location{Latitude: 48.85}})
	require.NoError(t, err)

	assert.Contains(t, string(b), `"latitude":48.85`)
	assert.NotContains(t, string(b), "speed")
}

func TestEventRoundTrip_PreservesNumericAndBoolFields(t *testing.T) {
	original := Event{
		ID:       "evt_1",
		Name:     "subscribed",
		Location: Location{City: "Paris", Latitude: 48.85, Longitude: 2.35},
		Network:  Network{WIFI: true, Carrier: "orange"},
		Screen:   Screen{Width: 1920, Height: 1080},
	}

	b, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Event
	require.NoError(t, json.Unmarshal(b, &decoded))

	assert.Equal(t, original, decoded)
}

// TestUnmarshal_V2RejectsDuplicateObjectMembers documents a behaviour change that
// is visible to consumers: v1 silently accepted duplicate JSON object keys and kept
// the last one, v2 rejects the document.
func TestUnmarshal_V2RejectsDuplicateObjectMembers(t *testing.T) {
	_, ok := FromJSON([]byte(`{"event":{"name":"a"},"event":{"name":"b"}}`))
	assert.False(t, ok, "a document with duplicate members must not decode")

	var e Event
	err := json.Unmarshal([]byte(`{"name":"a","name":"b"}`), &e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

// TestUnmarshal_V2RejectsInvalidUTF8 documents the second consumer-visible
// strictness change: v1 replaced invalid UTF-8 with U+FFFD, v2 rejects it.
func TestUnmarshal_V2RejectsInvalidUTF8(t *testing.T) {
	var e Event
	err := json.Unmarshal([]byte("{\"name\":\"\xff\xfe\"}"), &e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UTF-8")

	_, ok := FromJSON([]byte("{\"event\":{\"name\":\"\xff\"}}"))
	assert.False(t, ok)
}

func TestMarshal_V2RejectsInvalidUTF8(t *testing.T) {
	_, err := json.Marshal(Event{Name: "\xff"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UTF-8")
}

func TestFromJSON_AcceptsAnyNamedByteSlice(t *testing.T) {
	// The parameter is []byte, so a named []byte type still passes directly.
	type rawMessage []byte

	e, ok := FromJSON(rawMessage(`{"event":{"name":"subscribed"}}`))
	require.True(t, ok)
	assert.Equal(t, "subscribed", e.Name)

	e, ok = FromJSON([]byte(strings.TrimSpace(` {"event":{"name":"x"}} `)))
	require.True(t, ok)
	assert.Equal(t, "x", e.Name)
}
