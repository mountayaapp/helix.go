package valkey

import (
	"time"
)

// OptionsSet is used to set options when setting a key, with Set or SetNX.
type OptionsSet struct {

	// TTL specifies the Time To Live of the key. It is sent in milliseconds,
	// rounded up, so that a positive TTL below a millisecond still expires the key
	// rather than being rejected by the server.
	//
	// Zero or negative stores the key without expiry. Like any write of a whole
	// value, that also clears a TTL the key previously had.
	TTL time.Duration `json:"ttl,omitzero"`
}

// OptionsSAdd is used to set options when adding members to a set.
type OptionsSAdd struct {

	// TTL, when positive, is the set's time-to-live, sent in milliseconds rounded
	// up and set in the same round trip as the members, on every call. A set
	// whose members are refreshed by writing them again then lives as long as
	// they are written, and expires once nobody does.
	//
	// Zero or negative leaves the set's expiry as it was.
	TTL time.Duration `json:"ttl,omitzero"`
}

// OptionsGet is used to set options when retrieving a key.
type OptionsGet struct {

	// ErrorRecordOnNotFound indicates if an error should be recorded in traces in
	// case the key is missing and was not found in Valkey. Any other failure is
	// always recorded.
	ErrorRecordOnNotFound bool `json:"error_record_on_not_found,omitzero"`
}

// OptionsScan is used to set options when scanning keys.
type OptionsScan struct {

	// Count is how many keys the server examines per round trip. It is a hint, and
	// it trades the number of round trips against how long each one occupies the
	// server.
	//
	// Default:
	//
	//   1000
	Count int64 `json:"count,omitzero"`

	// Limit caps how many keys Scan returns, and stops the scan once that many are
	// found. A result of exactly Limit keys may therefore not be every match. Zero
	// returns every match, which on a large key space means holding all of them in
	// memory at once.
	Limit int `json:"limit,omitzero"`
}

// OptionsSScan is used to set options when scanning the members of a set.
type OptionsSScan struct {

	// Match keeps only the members matching this glob-style pattern. The server
	// matches a page after reading it, so a page may hold fewer members than Count,
	// or none, while the scan is not done. Empty keeps every member.
	Match string `json:"match,omitzero"`

	// Count is how many members the server examines per page. It is a hint: a page
	// of a small set holds the whole set, whatever Count is, and a page of a large
	// one may hold a few more or fewer. It trades the number of round trips against
	// the size of a page.
	//
	// Default:
	//
	//   100
	Count int64 `json:"count,omitzero"`
}

// OptionsXRange is used to set options when reading a range of a stream.
type OptionsXRange struct {

	// Count caps how many entries are returned, from the start of the range. Zero
	// returns the whole range.
	Count int64 `json:"count,omitzero"`
}
