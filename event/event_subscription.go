package event

import (
	"fmt"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/baggage"
)

/*
Subscription holds the details about the account/customer from which the event
has been triggered. It's useful for tracking customer usages.
*/
type Subscription struct {
	Id          string            `json:"id,omitempty"`
	TenantId    string            `json:"tenant_id,omitempty"`
	CustomerId  string            `json:"customer_id,omitempty"`
	ProductId   string            `json:"product_id,omitempty"`
	PriceId     string            `json:"price_id,omitempty"`
	Usage       string            `json:"usage,omitempty"`
	IncrementBy float64           `json:"increment_by,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

/*
injectEventSubscriptionsToFlatMap injects values found in a slice of Subscription
objects to a flat map representation of an Event.
*/
func injectEventSubscriptionsToFlatMap(subs []Subscription, flatten map[string]string) {
	if flatten == nil {
		flatten = make(map[string]string)
	}

	for i, sub := range subs {
		if sub.Id != "" {
			flatten[fmt.Sprintf("event.subscriptions.%d.id", i)] = sub.Id
		}

		if sub.TenantId != "" {
			flatten[fmt.Sprintf("event.subscriptions.%d.tenant_id", i)] = sub.TenantId
		}

		if sub.CustomerId != "" {
			flatten[fmt.Sprintf("event.subscriptions.%d.customer_id", i)] = sub.CustomerId
		}

		if sub.ProductId != "" {
			flatten[fmt.Sprintf("event.subscriptions.%d.product_id", i)] = sub.ProductId
		}

		if sub.PriceId != "" {
			flatten[fmt.Sprintf("event.subscriptions.%d.price_id", i)] = sub.PriceId
		}

		if sub.Usage != "" {
			flatten[fmt.Sprintf("event.subscriptions.%d.usage", i)] = sub.Usage
		}

		if sub.IncrementBy != 0 {
			flatten[fmt.Sprintf("event.subscriptions.%d.increment_by", i)] = fmt.Sprintf("%f", sub.IncrementBy)
		}

		if sub.Metadata != nil {
			for k, v := range sub.Metadata {
				flatten[fmt.Sprintf("event.subscriptions.%d.metadata.%s", i, k)] = v
			}
		}
	}
}

/*
applyEventSubscriptionsFromBaggageMember extracts the value of a Baggage member
given its key and applies it to an Event's Subscriptions. This assumes the Baggage
member's key starts with "event.subscriptions.".
*/
func applyEventSubscriptionsFromBaggageMember(m baggage.Member, e *Event) {
	split := strings.Split(m.Key(), ".")

	// Make sure to append a new subscription if the index found is greater than
	// the current length of the Subscriptions slice. Since the Baggage members
	// are not ordered, a key with index 1 may be called before one with index 0,
	// such as "event.subscriptions[1].id" called before "event.subscriptions[0].id".
	i, _ := strconv.Atoi(split[2])
	for i > len(e.Subscriptions)-1 {
		e.Subscriptions = append(e.Subscriptions, Subscription{})
	}

	switch split[3] {
	case "id":
		e.Subscriptions[i].Id = m.Value()
	case "tenant_id":
		e.Subscriptions[i].TenantId = m.Value()
	case "customer_id":
		e.Subscriptions[i].CustomerId = m.Value()
	case "product_id":
		e.Subscriptions[i].ProductId = m.Value()
	case "price_id":
		e.Subscriptions[i].PriceId = m.Value()
	case "usage":
		e.Subscriptions[i].Usage = m.Value()
	case "increment_by":
		e.Subscriptions[i].IncrementBy, _ = strconv.ParseFloat(m.Value(), 64)
	case "metadata":
		if e.Subscriptions[i].Metadata == nil {
			e.Subscriptions[i].Metadata = make(map[string]string)
		}

		e.Subscriptions[i].Metadata[split[4]] = m.Value()
	}
}
