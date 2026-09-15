package valkey

import (
	"time"
)

// OptionsSet is used to set options when setting a key.
type OptionsSet struct {

	// TTL specifies the Time To Live of the key.
	TTL time.Duration `json:"ttl,omitzero"`
}

// OptionsGet is used to set options when retrieving a key.
type OptionsGet struct {

	// ErrorRecordOnNotFound indicates if an error should be recorded in traces in
	// case the key is missing and was not found in Valkey.
	ErrorRecordOnNotFound bool `json:"error_record_on_not_found,omitzero"`
}
