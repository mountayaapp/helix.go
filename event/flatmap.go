package event

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ToFlatMap serializes an Event to a flat map using baggage struct tags. Keys are
// prefixed with "event." and struct levels are separated by ".". All values are
// stringified. Zero values are omitted.
//
// Example:
//
//	Event{
//	  Name:   "subscribed",
//	  UserID: "user_2N6YZQLcYy2SPtmHiII69yHp0WE",
//	  Subscriptions: []Subscription{
//	    {
//	      ID:         "sub_2N6YZQXgQAv87zMmvlHxePCSsRs",
//	      CustomerID: "cus_2N6YZMi3sBDPQBZrZJoYBwhNQNv",
//	    },
//	  },
//	}
//
// Will produce:
//
//	map[string]string{
//	  "event.name":                        "subscribed",
//	  "event.user_id":                     "user_2N6YZQLcYy2SPtmHiII69yHp0WE",
//	  "event.subscriptions.0.id":          "sub_2N6YZQXgQAv87zMmvlHxePCSsRs",
//	  "event.subscriptions.0.customer_id": "cus_2N6YZMi3sBDPQBZrZJoYBwhNQNv",
//	}
//
// ToFlatMap is the inverse of FromFlatMap, which is how an Event is rebuilt from
// the baggage an upstream service sent. It exists to complete that pair, for
// interoperating with a producer that speaks the same wire shape.
//
// It is not how helix propagates an Event, and must not be used as though it
// were. It walks the whole struct with reflection — every field, including
// UserID, IP and UserAgent — so building baggage from it ships personal data to
// every endpoint the process calls, third parties included, and overruns the
// 8,192-byte W3C budget on a populated Event. Event.Propagated is the outbound
// direction: an allow-list, non-reflective, and budget-aware.
func ToFlatMap(e Event) map[string]string {
	m := make(map[string]string)
	flattenValue(reflect.ValueOf(e), "event", m)
	return m
}

// maxFlatMapSliceLen caps the number of elements FromFlatMap builds for a slice
// field. Keys indexing past it are ignored. It matches the 64 members a W3C
// baggage header may carry, so no map built from one is ever truncated by it.
const maxFlatMapSliceLen = 64

// FromFlatMap deserializes an Event from a flat map using baggage struct tags.
// Keys must be prefixed with "event." and use "." as the level separator. Slice
// fields hold at most 64 elements: keys indexing past that are ignored.
//
// It decodes every tagged field, personal data included, and is therefore not how
// helix reads inbound baggage — FromContext reads only the allow-listed fields
// Propagated sends. FromFlatMap is for a map the caller already trusts.
func FromFlatMap(m map[string]string) Event {
	var e Event
	unflattenValue(reflect.ValueOf(&e).Elem(), "event", m)
	return e
}

// flattenValue recursively walks a struct value and writes its non-zero fields
// into the flat map with dot-separated keys derived from baggage struct tags.
func flattenValue(v reflect.Value, prefix string, m map[string]string) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		tag := field.Tag.Get("baggage")
		if tag == "" || tag == "-" {
			continue
		}

		key := prefix + "." + tag
		fv := v.Field(i)

		switch field.Type.Kind() {
		case reflect.String:
			if s := fv.String(); s != "" {
				m[key] = s
			}

		case reflect.Bool:
			// Only serialize true; false is the zero value and is omitted to
			// reduce payload size. Missing keys default to false on deserialization.
			if fv.Bool() {
				m[key] = "true"
			}

		case reflect.Int64:
			if n := fv.Int(); n != 0 {
				m[key] = strconv.FormatInt(n, 10)
			}

		case reflect.Float64:
			if f := fv.Float(); f != 0 {
				m[key] = strconv.FormatFloat(f, 'f', -1, 64)
			}

		case reflect.Pointer:
			if !fv.IsNil() {
				switch field.Type.Elem().Kind() {
				case reflect.Bool:
					m[key] = strconv.FormatBool(fv.Elem().Bool())
				}
			}

		case reflect.Struct:
			if field.Type == reflect.TypeOf(time.Time{}) {
				if ts := fv.Interface().(time.Time); !ts.IsZero() {
					m[key] = ts.Format(time.RFC3339Nano)
				}
			} else {
				flattenValue(fv, key, m)
			}

		case reflect.Slice:
			for j := range fv.Len() {
				elem := fv.Index(j)
				elemPrefix := fmt.Sprintf("%s.%d", key, j)
				if elem.Kind() == reflect.Struct {
					flattenValue(elem, elemPrefix, m)
				}
			}

		case reflect.Map:
			if field.Type.Key().Kind() == reflect.String && field.Type.Elem().Kind() == reflect.String {
				iter := fv.MapRange()
				for iter.Next() {
					mk := iter.Key().String()
					mv := iter.Value().String()
					if mv != "" {
						m[key+"."+mk] = mv
					}
				}
			}
		}
	}
}

// unflattenValue recursively walks a struct type and populates its fields from the
// flat map using baggage struct tags to determine which keys to read.
func unflattenValue(v reflect.Value, prefix string, m map[string]string) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		tag := field.Tag.Get("baggage")
		if tag == "" || tag == "-" {
			continue
		}

		key := prefix + "." + tag
		fv := v.Field(i)

		switch field.Type.Kind() {
		case reflect.String:
			if val, ok := m[key]; ok {
				fv.SetString(val)
			}

		case reflect.Bool:
			if val, ok := m[key]; ok {
				b, err := strconv.ParseBool(val)
				if err == nil {
					fv.SetBool(b)
				}
			}

		case reflect.Int64:
			if val, ok := m[key]; ok {
				n, err := strconv.ParseInt(val, 10, 64)
				if err == nil {
					fv.SetInt(n)
				}
			}

		case reflect.Float64:
			if val, ok := m[key]; ok {
				f, err := strconv.ParseFloat(val, 64)
				if err == nil {
					fv.SetFloat(f)
				}
			}

		case reflect.Pointer:
			if val, ok := m[key]; ok {
				switch field.Type.Elem().Kind() {
				case reflect.Bool:
					b, err := strconv.ParseBool(val)
					if err == nil {
						ptr := reflect.New(field.Type.Elem())
						ptr.Elem().SetBool(b)
						fv.Set(ptr)
					}
				}
			}

		case reflect.Struct:
			if field.Type == reflect.TypeOf(time.Time{}) {
				if val, ok := m[key]; ok {
					ts, err := time.Parse(time.RFC3339Nano, val)
					if err == nil {
						fv.Set(reflect.ValueOf(ts))
					}
				}
			} else {
				unflattenValue(fv, key, m)
			}

		case reflect.Slice:
			if field.Type.Elem().Kind() == reflect.Struct {
				slicePrefix := key + "."
				maxIdx := -1
				for k := range m {
					if !strings.HasPrefix(k, slicePrefix) {
						continue
					}

					rest := k[len(slicePrefix):]
					dotIdx := strings.Index(rest, ".")
					if dotIdx == -1 {
						continue
					}

					// An index past the cap is ignored rather than honoured: the slice is
					// sized from the largest index seen, so an uncapped one would let a
					// single key decide how much memory the decode allocates.
					idx, err := strconv.Atoi(rest[:dotIdx])
					if err != nil || idx < 0 || idx >= maxFlatMapSliceLen {
						continue
					}

					if idx > maxIdx {
						maxIdx = idx
					}
				}

				if maxIdx >= 0 {
					slice := reflect.MakeSlice(field.Type, maxIdx+1, maxIdx+1)
					for j := range maxIdx + 1 {
						elemPrefix := fmt.Sprintf("%s.%d", key, j)
						unflattenValue(slice.Index(j), elemPrefix, m)
					}

					fv.Set(slice)
				}
			}

		case reflect.Map:
			if field.Type.Key().Kind() == reflect.String && field.Type.Elem().Kind() == reflect.String {
				mapPrefix := key + "."
				for k, val := range m {
					if !strings.HasPrefix(k, mapPrefix) {
						continue
					}

					mapKey := k[len(mapPrefix):]
					if strings.Contains(mapKey, ".") {
						continue
					}

					if fv.IsNil() {
						fv.Set(reflect.MakeMap(field.Type))
					}

					fv.SetMapIndex(reflect.ValueOf(mapKey), reflect.ValueOf(val))
				}
			}
		}
	}
}
