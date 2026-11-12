package rest

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

// The limits a request-validation refusal is held to, so that its size is set by
// these numbers and never by the request it refuses.
//
// A client decides how many values of its request fail, how deep they sit, what
// its members, parts and parameters are called and what they hold, and the
// validators quote much of that in what they report. Without a limit a refusal
// grows with the request, and can outgrow it: a part's name comes back whole, and
// a header line the decoder quotes comes back several times its size.
//
// The limits are set above what an ordinary refusal holds, so that one reads as
// it always did, and what one of them leaves out is marked, so that a reader knows
// something was: a value or a message cut ends with cutMarker, a path cut has it
// as its last segment, a list of names says how many more there were, and entries
// left out are counted by one more entry (moreProblemsMessage).
const (
	// maxRefusalBytes is how much a refusal's entries weigh together at most, as
	// the envelope writes them (entryWeight): the first entries, in the order a
	// refusal lists them, until the next would pass it. It is what bounds the
	// answer as a whole, whatever each entry holds. An entry heavier than it on its
	// own is listed all the same, cut until it fits (fitEntry).
	maxRefusalBytes = 64 << 10

	// maxRefusalMessageBytes is how long an entry's message is at most. It is
	// twice maxQuotedBytes, room for a value quoted whole beside what the
	// description makes a message say (a pattern, the values of an enum), and
	// every message is held to it, including one this package has no rule of its
	// own for.
	maxRefusalMessageBytes = 8 << 10

	// maxRefusalPathSegments is how many segments an entry's path has at most. A
	// value nested deeper is located by the first ones, which is the value it
	// sits in, and its path ends with a segment of its own, cutMarker, in place of
	// the rest.
	maxRefusalPathSegments = 32

	// maxQuotedBytes is how much of one name or value the request supplied is
	// written into a path or a message: the name of a member, a part or a query
	// parameter, the value of a parameter that does not parse, the string a
	// pattern refuses, a Content-Type. Ordinary ones are far shorter: a browser's
	// multipart Content-Type is 68 to 90 bytes and one RFC 2046 allows at most 100,
	// and a URL is kept under 2 KiB by browsers and servers alike, a description
	// commonly bounding one at 2,048 bytes. 4 KiB quotes whole a value one byte
	// past such a bound.
	maxQuotedBytes = 4 << 10

	// maxRefusalNames is how many of the names the request supplied one message
	// lists.
	maxRefusalNames = 16
)

// replacementCharacter is what bytes that are not UTF-8 are written as.
const replacementCharacter = "\uFFFD"

// cutMarker ends a value or a message cut to its limit.
const cutMarker = "\u2026"

// moreProblemsMessage returns the message of the entry counting the n problems a
// refusal does not list. The entry has no path: it is about no value of the
// request.
func moreProblemsMessage(n int) string {
	if n == 1 {
		return "1 more problem is not listed"
	}

	return fmt.Sprintf("%d more problems are not listed", n)
}

// moreProblemsWeight is what the entry counting the problems left out weighs at
// most, kept free under maxRefusalBytes for it.
const moreProblemsWeight = 128

// boundEntries returns entries as a refusal carries them: each message no longer
// than maxRefusalMessageBytes, each path held by boundPath, every string valid
// UTF-8, and no more of them than weigh maxRefusalBytes together. When some are
// left out, one more entry, with no path, says how many (moreProblemsMessage); the
// entries listed keep their paths. When the first entry alone weighs more than
// the bound, it is listed cut until it fits (fitEntry) rather than counted, so a
// refusal always says something of what was wrong.
//
// It is the last thing a refusal goes through, whatever built its entries, so
// the limits hold for a failure no rule of this package anticipated: a message
// of the validator's that quotes the request is still cut here. It is also what
// keeps a refusal a 400: the envelope does not marshal a string that is not
// UTF-8, and a name the client spelled in other bytes would otherwise turn the
// refusal into a 500.
func boundEntries(entries []errorstack.Entry) []errorstack.Entry {
	weight := 0
	for i := range entries {
		entries[i].Message = boundText(entries[i].Message, maxRefusalMessageBytes)
		entries[i].Path = boundPath(entries[i].Path)

		// Room is kept for the entry counting the ones left out, unless none is.
		budget := maxRefusalBytes
		if i < len(entries)-1 {
			budget -= moreProblemsWeight
		}

		if entryWeight := entryWeight(entries[i]); weight+entryWeight <= budget {
			weight += entryWeight
			continue
		}

		listed := i
		if i == 0 {
			entries[0] = fitEntry(entries[0], budget)
			listed = 1
		}

		if listed == len(entries) {
			return entries
		}

		return append(entries[:listed:listed], errorstack.Entry{
			Message: moreProblemsMessage(len(entries) - listed),
		})
	}

	return entries
}

// entryWeight returns how many bytes entry takes in the envelope: the entry as
// errorstack.NewValidation makes it, its code among its extensions, written by
// the encoder the envelope is written with, and the comma that separates it from
// the next. An entry the encoder refuses weighs nothing here: the envelope
// refuses it too, and answers its own fallback.
func entryWeight(entry errorstack.Entry) int {
	extensions := make(map[string]any, len(entry.Extensions)+1)
	maps.Copy(extensions, entry.Extensions)
	extensions["code"] = errorstack.CodeValidationFailed
	entry.Extensions = extensions

	encoded, err := json.Marshal(entry)
	if err != nil {
		return 0
	}

	return len(encoded) + 1
}

// fitEntry returns entry, which weighs more than budget on its own, cut until it
// weighs no more. Its path keeps the first segments that fit in half of budget,
// ending with cutMarker in place of the rest, which keeps the value the problem is
// about located by the values it sits in. Its message keeps the longest beginning
// that fits what is left, cut on a character and marked (boundText).
func fitEntry(entry errorstack.Entry, budget int) errorstack.Entry {
	pathWeight := func(path []any) int {
		return entryWeight(errorstack.Entry{Path: path, Extensions: entry.Extensions})
	}

	if pathWeight(entry.Path) > budget/2 {
		kept := len(entry.Path) - 1
		for kept > 0 && pathWeight(append(slices.Clone(entry.Path[:kept]), cutMarker)) > budget/2 {
			kept--
		}

		entry.Path = append(slices.Clone(entry.Path[:kept]), cutMarker)
	}

	if entryWeight(entry) <= budget {
		return entry
	}

	message := entry.Message
	fits := func(limit int) bool {
		entry.Message = boundText(message, limit)
		return entryWeight(entry) <= budget
	}

	// The longest beginning of the message that fits: the search ends on a limit
	// that fits, len(cutMarker) at the least, where only the mark is left.
	low, high := len(cutMarker), len(message)
	for low < high {
		limit := (low + high + 1) / 2
		if fits(limit) {
			low = limit
		} else {
			high = limit - 1
		}
	}

	entry.Message = boundText(message, low)

	return entry
}

// boundPath returns path with no more than maxRefusalPathSegments segments: a
// longer one keeps its first ones and ends with cutMarker in place of the rest.
// Each segment that is a name is cut by boundName. The path it is given is left
// as it is.
func boundPath(path []any) []any {
	if len(path) == 0 {
		return path
	}

	cut := len(path) > maxRefusalPathSegments
	if cut {
		path = path[:maxRefusalPathSegments-1]
	}

	bounded := make([]any, len(path), len(path)+1)
	for i, segment := range path {
		if name, ok := segment.(string); ok {
			segment = boundName(name)
		}

		bounded[i] = segment
	}

	if cut {
		bounded = append(bounded, cutMarker)
	}

	return bounded
}

// boundName returns a name or a value the request supplied as it is written into
// a path or a message: valid UTF-8, and no longer than maxQuotedBytes, ending
// with cutMarker when it was cut.
func boundName(name string) string {
	return boundText(name, maxQuotedBytes)
}

// boundNames returns the names one message lists: each cut by boundName, sorted,
// a name listed once, and no more than maxRefusalNames of them, with how many
// more there were. Two names that differ only past the cut are one.
func boundNames(names []string) ([]string, int) {
	bounded := make([]string, len(names))
	for i, name := range names {
		bounded[i] = boundName(name)
	}

	slices.Sort(bounded)
	bounded = slices.Compact(bounded)

	if len(bounded) > maxRefusalNames {
		return bounded[:maxRefusalNames], len(bounded) - maxRefusalNames
	}

	return bounded, 0
}

// boundText returns text as valid UTF-8 of at most limit bytes. A text that is
// both already is returned as it is. Otherwise each run of bytes that are not
// UTF-8 becomes one U+FFFD, and a text still longer than limit is cut after the
// last whole character that fits with cutMarker after it, so a cut never falls
// inside a character and always shows.
func boundText(text string, limit int) string {
	if len(text) <= limit && utf8.ValidString(text) {
		return text
	}

	valid := strings.ToValidUTF8(text, replacementCharacter)
	if len(valid) <= limit {
		return valid
	}

	marker := cutMarker
	if limit < len(marker) {
		marker = ""
	}

	cut := limit - len(marker)
	for cut > 0 && !utf8.RuneStart(valid[cut]) {
		cut--
	}

	return valid[:cut] + marker
}

// parseFailureMessage returns the message of a value kin-openapi could not parse:
// a query parameter that is no integer, a form field that is no boolean, a part
// the form does not declare. It returns false when parseErr is about no such
// value and only passes on the error of the parser that read the body, which is
// not a client's to read (undecodableBodyMessage).
//
// The message is the one parseErr's own Error method gives, "path count: value
// many: an invalid integer: invalid syntax", built here from the error's fields
// rather than taken from it, because that method writes the value and the names
// on its path whole, and they are the request's: each is cut by boundName here.
func parseFailureMessage(parseErr *openapi3filter.ParseError) (string, bool) {
	chain := []*openapi3filter.ParseError{parseErr}
	for {
		cause, ok := chain[len(chain)-1].Cause.(*openapi3filter.ParseError) //nolint:errorlint // A link of the chain, never wrapped.
		if !ok {
			break
		}

		chain = append(chain, cause)
	}

	// kin-openapi says what is wrong with a value in the Reason of the innermost
	// error, beside the value. An innermost error with neither wraps something
	// else: the one sentence naming a part the form does not declare, or a
	// parser's error.
	leaf := chain[len(chain)-1]
	if leaf.Reason == "" && leaf.Value == nil {
		name, ok := undeclaredPart(leaf)
		if !ok {
			return "", false
		}

		return "part " + name + ": undefined", true
	}

	var parts []string

	if path := parseErr.Path(); len(path) > 0 {
		names := make([]string, 0, len(path))
		for _, segment := range boundPath(path) {
			if name, ok := segment.(string); ok {
				names = append(names, name)
			} else {
				names = append(names, boundName(fmt.Sprint(segment)))
			}
		}

		parts = append(parts, "path "+strings.Join(names, "."))
	}

	for _, link := range chain {
		switch value := link.Value.(type) {
		case nil:
		case string:
			parts = append(parts, "value "+boundName(value))
		default:
			parts = append(parts, "value "+boundName(fmt.Sprint(value)))
		}

		if link.Reason != "" {
			parts = append(parts, link.Reason)
		}
	}

	// Beside a Reason the cause is the sentinel of the conversion that failed
	// ("invalid syntax", "value out of range"), which quotes nothing, or the
	// error of a part's own Content-Type, which quotes it: it is held to the bound
	// a quoted value is.
	if leaf.Cause != nil {
		parts = append(parts, boundText(leaf.Cause.Error(), maxQuotedBytes))
	}

	return strings.Join(parts, ": "), true
}

// undeclaredPart returns the name of the multipart part leaf reports as one the
// form does not declare, cut by boundName, and false when leaf reports anything
// else.
//
// kin-openapi's multipart decoder stops at the first such part and reports it in
// a sentence of its own, "part <name>: undefined", with the name as the request
// spelled it and nowhere else. The name is whatever stands between the two ends
// of that sentence.
func undeclaredPart(leaf *openapi3filter.ParseError) (string, bool) {
	if leaf.Kind != openapi3filter.KindOther || leaf.Cause == nil {
		return "", false
	}

	name, found := strings.CutPrefix(leaf.Cause.Error(), "part ")
	if !found {
		return "", false
	}

	name, found = strings.CutSuffix(name, ": undefined")
	if !found {
		return "", false
	}

	return boundName(name), true
}

// schemaErrorMessage returns the message of a failure of kin-openapi's own
// validator, the one an OpenAPI 3.0 description is validated with: its reason, as
// it always was, but for the one reason that quotes a name the request chose,
// `property "<name>" is unsupported`, where the name is cut (boundQuoted).
//
// Error rather than Reason: the middleware installs a custom schema-error func
// that already reduces Error to the reason alone, and Error still says something
// when Reason is empty.
func schemaErrorMessage(schemaErr *openapi3.SchemaError) string {
	return boundQuoted(schemaErr.Error(), "property ", " is unsupported")
}

// unexpectedContentType is how kin-openapi starts the reason of a body sent under
// a media type the operation does not declare, before the Content-Type header as
// the request spelled it, quoted.
const unexpectedContentType = "header Content-Type has unexpected value "

// boundQuoted returns message with what it quotes cut by boundName, when message
// is prefix, a string quoted the way fmt's %q quotes one, then suffix: the shape
// of the messages kin-openapi builds around something the request supplied. Any
// other message is returned as it is, and so is one quoting a string that
// boundName leaves alone.
func boundQuoted(message, prefix, suffix string) string {
	quoted, found := strings.CutPrefix(message, prefix)
	if !found {
		return message
	}

	quoted, found = strings.CutSuffix(quoted, suffix)
	if !found {
		return message
	}

	supplied, err := strconv.Unquote(quoted)
	if err != nil {
		return message
	}

	return prefix + strconv.Quote(boundName(supplied)) + suffix
}
