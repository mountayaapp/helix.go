package event

import (
	"encoding/json"
)

/*
Key is the key that shall be present in a JSON-encoded value representing an Event.

Example:

	{
	  "key": "value",
	  "event": {
	    "name": "subscribed"
	  }
	}
*/
const Key string = "event"

/*
EventFromJSON returns the Event found at the "event" key in the JSON-encoded data
passed, if any. Returns true if an Event has been found, false otherwise.
*/
func EventFromJSON(input json.RawMessage) (Event, bool) {
	var wrapper struct {
		Event json.RawMessage `json:"event"`
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
