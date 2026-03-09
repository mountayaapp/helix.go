package event

import (
	"time"
)

/*
Event is a dictionary of information that provides useful context about an event.
An Event shall be present as much as possible when passing data across services,
allowing to better understand the origin of an event.

Event should be used for data that you're okay with potentially exposing to anyone
who inspects your network traffic. This is because it's stored in HTTP headers
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
	ID             string            `json:"id,omitempty"              baggage:"id"`
	Name           string            `json:"name,omitempty"            baggage:"name"`
	Meta           map[string]string `json:"meta,omitempty"            baggage:"meta"`
	IsAnonymous    *bool             `json:"is_anonymous,omitempty"    baggage:"is_anonymous"`
	UserID         string            `json:"user_id,omitempty"         baggage:"user_id"`
	OrganizationID string            `json:"organization_id,omitempty" baggage:"organization_id"`
	TenantID       string            `json:"tenant_id,omitempty"       baggage:"tenant_id"`
	IP             string            `json:"ip,omitempty"              baggage:"ip"`
	UserAgent      string            `json:"user_agent,omitempty"      baggage:"user_agent"`
	Locale         string            `json:"locale,omitempty"          baggage:"locale"`
	Timezone       string            `json:"timezone,omitempty"        baggage:"timezone"`
	Timestamp      time.Time         `json:"timestamp,omitzero"        baggage:"timestamp"`
	App            App               `json:"app,omitzero"              baggage:"app"`
	Campaign       Campaign          `json:"campaign,omitzero"         baggage:"campaign"`
	Device         Device            `json:"device,omitzero"           baggage:"device"`
	Location       Location          `json:"location,omitzero"         baggage:"location"`
	Network        Network           `json:"network,omitzero"          baggage:"network"`
	OS             OS                `json:"os,omitzero"               baggage:"os"`
	Page           Page              `json:"page,omitzero"             baggage:"page"`
	Referrer       Referrer          `json:"referrer,omitzero"         baggage:"referrer"`
	Screen         Screen            `json:"screen,omitzero"           baggage:"screen"`
	Subscriptions  []Subscription    `json:"subscriptions,omitempty"   baggage:"subscriptions"`
}

/*
BoolPtr returns a pointer to the given bool value. This is a convenience helper
for setting Event.IsAnonymous.
*/
func BoolPtr(v bool) *bool {
	return &v
}

/*
App holds the details about the client application executing the event.
*/
type App struct {
	Name    string `json:"name,omitempty"     baggage:"name"`
	Version string `json:"version,omitempty"  baggage:"version"`
	BuildID string `json:"build_id,omitempty" baggage:"build_id"`
}

/*
Campaign holds the details about the marketing campaign from which a client is
executing the event from.
*/
type Campaign struct {
	Name    string `json:"name,omitempty"    baggage:"name"`
	Source  string `json:"source,omitempty"  baggage:"source"`
	Medium  string `json:"medium,omitempty"  baggage:"medium"`
	Term    string `json:"term,omitempty"    baggage:"term"`
	Content string `json:"content,omitempty" baggage:"content"`
}

/*
Device holds the details about the user's device.
*/
type Device struct {
	ID            string `json:"id,omitempty"             baggage:"id"`
	Manufacturer  string `json:"manufacturer,omitempty"   baggage:"manufacturer"`
	Model         string `json:"model,omitempty"          baggage:"model"`
	Name          string `json:"name,omitempty"           baggage:"name"`
	Type          string `json:"type,omitempty"           baggage:"type"`
	Version       string `json:"version,omitempty"        baggage:"version"`
	AdvertisingID string `json:"advertising_id,omitempty" baggage:"advertising_id"`
}

/*
Location holds the details about the user's location.
*/
type Location struct {
	City      string  `json:"city,omitempty"      baggage:"city"`
	Country   string  `json:"country,omitempty"   baggage:"country"`
	Region    string  `json:"region,omitempty"    baggage:"region"`
	Latitude  float64 `json:"latitude,omitempty"  baggage:"latitude"`
	Longitude float64 `json:"longitude,omitempty" baggage:"longitude"`
	Speed     float64 `json:"speed,omitempty"     baggage:"speed"`
}

/*
Network holds the details about the user's network.
*/
type Network struct {
	Bluetooth bool   `json:"bluetooth,omitempty" baggage:"bluetooth"`
	Cellular  bool   `json:"cellular,omitempty"  baggage:"cellular"`
	WIFI      bool   `json:"wifi,omitempty"      baggage:"wifi"`
	Carrier   string `json:"carrier,omitempty"   baggage:"carrier"`
}

/*
OS holds the details about the user's OS.
*/
type OS struct {
	Name    string `json:"name,omitempty"    baggage:"name"`
	Arch    string `json:"arch,omitempty"    baggage:"arch"`
	Version string `json:"version,omitempty" baggage:"version"`
}

/*
Page holds the details about the webpage from which the event is triggered from.
*/
type Page struct {
	Path     string `json:"path,omitempty"     baggage:"path"`
	Referrer string `json:"referrer,omitempty" baggage:"referrer"`
	Search   string `json:"search,omitempty"   baggage:"search"`
	Title    string `json:"title,omitempty"    baggage:"title"`
	URL      string `json:"url,omitempty"      baggage:"url"`
}

/*
Referrer holds the details about the marketing referrer from which a client is
executing the event from.
*/
type Referrer struct {
	Type string `json:"type,omitempty" baggage:"type"`
	Name string `json:"name,omitempty" baggage:"name"`
	URL  string `json:"url,omitempty"  baggage:"url"`
	Link string `json:"link,omitempty" baggage:"link"`
}

/*
Screen holds the details about the app's screen from which the event is triggered
from.
*/
type Screen struct {
	Density int64 `json:"density,omitempty" baggage:"density"`
	Width   int64 `json:"width,omitempty"   baggage:"width"`
	Height  int64 `json:"height,omitempty"  baggage:"height"`
}

/*
Subscription holds the details about the account/customer from which the event
has been triggered. It's useful for tracking customer usages.
*/
type Subscription struct {
	ID          string            `json:"id,omitempty"           baggage:"id"`
	TenantID    string            `json:"tenant_id,omitempty"    baggage:"tenant_id"`
	CustomerID  string            `json:"customer_id,omitempty"  baggage:"customer_id"`
	ProductID   string            `json:"product_id,omitempty"   baggage:"product_id"`
	PriceID     string            `json:"price_id,omitempty"     baggage:"price_id"`
	Usage       string            `json:"usage,omitempty"        baggage:"usage"`
	IncrementBy float64           `json:"increment_by,omitempty" baggage:"increment_by"`
	Metadata    map[string]string `json:"metadata,omitempty"     baggage:"metadata"`
}
