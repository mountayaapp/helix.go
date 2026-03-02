package event

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/baggage"
)

/*
Event is a dictionary of information that provides useful context about an event.
An Event shall be present as much as possible when passing data across services,
allowing to better understand the origin of an event.

Event should be used for data that you’re okay with potentially exposing to anyone
who inspects your network traffic. This is because it’s stored in HTTP headers
for distributed tracing. If your relevant network traffic is entirely within your
own network, then this caveat may not apply.

This is heavily inspired by the following references, and was adapted to better
fit this ecosystem:

  - The Segment's Context described at:
    https://segment.com/docs/connections/spec/common/#context
  - The Elastic Common Schema described at:
    https://www.elastic.co/guide/en/ecs/current/ecs-field-reference.html
*/
type Event struct {
	Id             string            `json:"id,omitempty"`
	Name           string            `json:"name,omitempty"`
	Meta           map[string]string `json:"meta,omitempty"`
	Params         url.Values        `json:"params,omitempty"`
	IsAnonymous    bool              `json:"is_anonymous"`
	UserId         string            `json:"user_id,omitempty"`
	OrganizationId string            `json:"organization_id,omitempty"`
	TenantId       string            `json:"tenant_id,omitempty"`
	IP             net.IP            `json:"ip,omitempty"`
	UserAgent      string            `json:"user_agent,omitempty"`
	Locale         string            `json:"locale,omitempty"`
	Timezone       string            `json:"timezone,omitempty"`
	Timestamp      time.Time         `json:"timestamp,omitzero"`
	App            App               `json:"app,omitzero"`
	Campaign       Campaign          `json:"campaign,omitzero"`
	Device         Device            `json:"device,omitzero"`
	Location       Location          `json:"location,omitzero"`
	Network        Network           `json:"network,omitzero"`
	OS             OS                `json:"os,omitzero"`
	Page           Page              `json:"page,omitzero"`
	Referrer       Referrer          `json:"referrer,omitzero"`
	Screen         Screen            `json:"screen,omitzero"`
	Subscriptions  []Subscription    `json:"subscriptions,omitempty"`
}

/*
injectEventToFlatMap injects values found in an Event object to a flat map
representation of an Event. Top-level keys are handled here, while objects
are handled in their own functions for better clarity and maintainability.
*/
func injectEventToFlatMap(e Event, flatten map[string]string) {
	if flatten == nil {
		flatten = make(map[string]string)
	}

	flatten["event.id"] = e.Id
	flatten["event.name"] = e.Name

	if e.Meta != nil {
		for k, v := range e.Meta {
			flatten[fmt.Sprintf("event.meta.%s", k)] = v
		}
	}

	if e.Params != nil {
		for k, v := range e.Params {
			split := strings.Split(k, ".")
			for i, s := range v {
				flatten[fmt.Sprintf("event.params.%s.%d", split[0], i)] = s
			}
		}
	}

	flatten["event.is_anonymous"] = strconv.FormatBool(e.IsAnonymous)
	flatten["event.user_id"] = e.UserId
	flatten["event.organization_id"] = e.OrganizationId
	flatten["event.tenant_id"] = e.TenantId
	flatten["event.ip"] = e.IP.String()
	flatten["event.user_agent"] = e.UserAgent
	flatten["event.locale"] = e.Locale
	flatten["event.timezone"] = e.Timezone
	if !e.Timestamp.IsZero() {
		flatten["event.timestamp"] = e.Timestamp.Format(time.RFC3339Nano)
	}

	injectEventAppToFlatMap(e.App, flatten)
	injectEventCampaignToFlatMap(e.Campaign, flatten)
	injectEventDeviceToFlatMap(e.Device, flatten)
	injectEventLocationToFlatMap(e.Location, flatten)
	injectEventNetworkToFlatMap(e.Network, flatten)
	injectEventOSToFlatMap(e.OS, flatten)
	injectEventPageToFlatMap(e.Page, flatten)
	injectEventReferrerToFlatMap(e.Referrer, flatten)
	injectEventScreenToFlatMap(e.Screen, flatten)
	injectEventSubscriptionsToFlatMap(e.Subscriptions, flatten)

	for k, v := range flatten {
		if v == "" || v == "false" || v == "0" || v == "0E+00" || v == "0.000000" || v == "<nil>" {
			delete(flatten, k)
		}
	}
}

/*
extractEventFromBaggage extracts the value of a Baggage and returns the Event
found. This assumes the Baggage members' key starts with "event.". Top-level keys
are handled here, while objects are handled in their own functions for better
clarity and maintainability.
*/
func extractEventFromBaggage(b baggage.Baggage) Event {
	var e Event

	for _, m := range b.Members() {
		if !strings.HasPrefix(m.Key(), "event.") {
			continue
		}

		if strings.HasPrefix(m.Key(), "event.meta.") {
			if e.Meta == nil {
				e.Meta = make(map[string]string)
			}

			e.Meta[strings.TrimPrefix(m.Key(), "event.meta.")] = m.Value()
			continue
		}

		if strings.HasPrefix(m.Key(), "event.params.") {
			if e.Params == nil {
				e.Params = make(url.Values)
			}

			keyWithIndex := strings.TrimPrefix(m.Key(), "event.params.")
			if idx := strings.Index(keyWithIndex, "."); idx != -1 {
				paramKey := keyWithIndex[:idx]
				indexStr := keyWithIndex[idx+1:]
				i, _ := strconv.Atoi(indexStr)

				for len(e.Params[paramKey]) <= i {
					e.Params[paramKey] = append(e.Params[paramKey], "")
				}

				e.Params[paramKey][i] = m.Value()
			} else {
				e.Params.Add(keyWithIndex, m.Value())
			}

			continue
		}

		split := strings.Split(m.Key(), ".")
		switch split[1] {
		case "id":
			e.Id = b.Member("event.id").Value()
		case "name":
			e.Name = b.Member("event.name").Value()
		case "is_anonymous":
			e.IsAnonymous, _ = strconv.ParseBool(b.Member("event.is_anonymous").Value())
		case "user_id":
			e.UserId = b.Member("event.user_id").Value()
		case "organization_id":
			e.OrganizationId = b.Member("event.organization_id").Value()
		case "tenant_id":
			e.TenantId = b.Member("event.tenant_id").Value()
		case "ip":
			e.IP = net.ParseIP(b.Member("event.ip").Value())
		case "user_agent":
			e.UserAgent = b.Member("event.user_agent").Value()
		case "locale":
			e.Locale = b.Member("event.locale").Value()
		case "timezone":
			e.Timezone = b.Member("event.timezone").Value()
		case "timestamp":
			e.Timestamp, _ = time.Parse(time.RFC3339Nano, b.Member("event.timestamp").Value())

		case "app":
			applyEventAppFromBaggageMember(m, &e)
		case "campaign":
			applyEventCampaignFromBaggageMember(m, &e)
		case "device":
			applyEventDeviceFromBaggageMember(m, &e)
		case "location":
			applyEventLocationFromBaggageMember(m, &e)
		case "network":
			applyEventNetworkFromBaggageMember(m, &e)
		case "os":
			applyEventOSFromBaggageMember(m, &e)
		case "page":
			applyEventPageFromBaggageMember(m, &e)
		case "referrer":
			applyEventReferrerFromBaggageMember(m, &e)
		case "screen":
			applyEventScreenFromBaggageMember(m, &e)
		case "subscriptions":
			applyEventSubscriptionsFromBaggageMember(m, &e)
		}
	}

	return e
}
