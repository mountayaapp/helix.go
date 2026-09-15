package event

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// Key is the key that shall be present in a JSON-encoded value representing an Event.
//
// Example:
//
//	{
//	  "key": "value",
//	  "event": {
//	    "name": "subscribed"
//	  }
//	}
const Key string = "event"

// FromJSON returns the Event found at the "event" key in the JSON-encoded data
// passed, if any. Returns true if an Event has been found, false otherwise.
//
// input is plain JSON-encoded bytes, so any named []byte type (such as the v1
// encoding/json.RawMessage) may be passed directly.
func FromJSON(input []byte) (Event, bool) {
	var wrapper struct {
		Event jsontext.Value `json:"event"`
	}

	if err := json.Unmarshal(input, &wrapper); err != nil {
		return Event{}, false
	}

	if wrapper.Event == nil {
		return Event{}, false
	}

	var e Event
	if err := json.Unmarshal(wrapper.Event, &e); err != nil {
		return Event{}, false
	}

	return e, true
}
