package valkey

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valkey-io/valkey-go"
)

// ErrSubscriberTooSlow is the error Subscribe returns when its handler fell
// further behind its channel than Config.SubscriberBacklog or
// Config.SubscriberBacklogBytes allow. The subscription is ended rather than
// left to hold up the connection: messages for every channel, and the replies to
// every command, arrive on the connection the subscriptions share, so a backlog
// nobody bounds is either memory without limit or a connection that stops
// reading. The messages still waiting for the handler are not delivered.
var ErrSubscriberTooSlow = errors.New("valkey: subscriber fell behind its channel")

// ErrSubscriptionInterrupted is the error Subscribe returns when the server
// subscription it was delivered through ended without the caller asking: the
// connection carrying it was lost, the server unsubscribed it, or the Service
// closed the connection at shutdown. A message published meanwhile was not
// delivered and will not be, since a channel delivers to the subscribers
// connected when it is published. A caller that must not miss one subscribes
// again and reads what it missed from a durable source.
var ErrSubscriptionInterrupted = errors.New("valkey: subscription interrupted")

const (

	// defaultSubscriberBacklog is Config.SubscriberBacklog's default.
	defaultSubscriberBacklog = 1024

	// defaultSubscriberBacklogBytes is Config.SubscriberBacklogBytes's default.
	defaultSubscriberBacklogBytes = 32 << 20

	// unsubscribeTimeout bounds the UNSUBSCRIBE sent when a channel's last local
	// subscriber leaves. The command is written whether or not its reply is
	// awaited to the end, so the bound only limits how long the connection's
	// reader can be held up by messages the server sent for the channel before
	// it read the command.
	unsubscribeTimeout = 500 * time.Millisecond
)

// pubsub is a connection's pub/sub hub. Every local Subscribe of a channel shares
// one server subscription, opened by the first and ended by the last, and each
// subscriber is handed its messages through a queue of its own. The driver hands
// a channel's messages over on a goroutine that also reads the replies to other
// commands on the same connection, so nothing it calls may block: a message is
// queued for each subscriber without waiting, and a subscriber whose queue is
// full is ended (ErrSubscriberTooSlow) rather than waited for.
type pubsub struct {
	mu       sync.Mutex
	channels map[string]*channelSubscription

	// backlog and backlogBytes are Config.SubscriberBacklog and
	// Config.SubscriberBacklogBytes.
	backlog      int
	backlogBytes int64
}

// channelSubscription is the one server subscription of a channel and the local
// subscribers it delivers to. Every field but channel and the channels' own
// synchronization is guarded by pubsub.mu.
type channelSubscription struct {
	channel string

	// cancel ends the Receive holding the server subscription.
	cancel context.CancelFunc

	// members are the local subscribers the channel's messages are queued for.
	members map[*subscriber]struct{}

	// confirmed is closed once the server confirms the subscription, after which
	// a message published is delivered to every member.
	confirmed chan struct{}

	// isConfirmed reports that confirmed is closed.
	isConfirmed bool

	// closing reports that the last member left and the server subscription is
	// being ended. A Subscribe of the channel meanwhile waits for done before it
	// opens a new one, so the UNSUBSCRIBE of the old one cannot end the new.
	closing bool

	// done is closed once the server subscription has ended and the channel is
	// out of pubsub.channels.
	done chan struct{}
}

// subscriber is one local Subscribe: the queue its messages wait in, and how its
// subscription ended when something other than its own ctx ended it.
type subscriber struct {
	queue chan valkey.PubSubMessage

	// ctx is the ctx handlers are called with, a child of Subscribe's own. It is
	// cancelled once the subscription ends, so a handler waiting on something
	// stops waiting for a subscription nobody delivers to any more.
	ctx    context.Context
	cancel context.CancelFunc

	// pending is the payload bytes waiting in queue.
	pending atomic.Int64

	// ended is closed when the subscription is ended for the subscriber, err
	// saying why. Both are written under pubsub.mu, before ended is closed.
	ended chan struct{}
	err   error
}

// newPubSub returns a hub queuing at most backlog messages, and backlogBytes
// bytes of payload, for each subscriber.
func newPubSub(backlog int, backlogBytes int64) *pubsub {
	return &pubsub{
		channels:     map[string]*channelSubscription{},
		backlog:      backlog,
		backlogBytes: backlogBytes,
	}
}

// join adds a subscriber to channel, opening the server subscription when the
// channel has none. A channel whose server subscription is still being ended is
// waited for first; join returns ctx's error when ctx ends meanwhile.
func (ps *pubsub) join(ctx context.Context, conn *connection, channel string) (*channelSubscription, *subscriber, error) {
	for {
		ps.mu.Lock()

		cs := ps.channels[channel]
		if cs != nil && cs.closing {
			done := cs.done
			ps.mu.Unlock()

			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}

		if cs == nil {
			receiveCtx, cancel := context.WithCancel(context.Background())
			cs = &channelSubscription{
				channel:   channel,
				cancel:    cancel,
				members:   map[*subscriber]struct{}{},
				confirmed: make(chan struct{}),
				done:      make(chan struct{}),
			}
			ps.channels[channel] = cs

			go ps.serve(receiveCtx, conn, cs)
		}

		sub := &subscriber{
			queue: make(chan valkey.PubSubMessage, ps.backlog),
			ended: make(chan struct{}),
		}
		sub.ctx, sub.cancel = context.WithCancel(ctx)
		cs.members[sub] = struct{}{}

		ps.mu.Unlock()

		return cs, sub, nil
	}
}

// leave removes sub from cs, ending the server subscription when sub was its last
// member. A subscriber already ended is left as it is.
func (ps *pubsub) leave(cs *channelSubscription, sub *subscriber) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sub.cancel()

	if _, member := cs.members[sub]; !member {
		return
	}

	delete(cs.members, sub)
	ps.releaseIfEmpty(cs)
}

// end ends sub's subscription with err, ending the server subscription when sub
// was its last member. The caller holds pubsub.mu.
func (ps *pubsub) end(cs *channelSubscription, sub *subscriber, err error) {
	delete(cs.members, sub)
	sub.err = err
	close(sub.ended)
	sub.cancel()

	ps.releaseIfEmpty(cs)
}

// releaseIfEmpty starts ending cs's server subscription once it has no member
// left. The caller holds pubsub.mu.
func (ps *pubsub) releaseIfEmpty(cs *channelSubscription) {
	if len(cs.members) > 0 || cs.closing {
		return
	}

	cs.closing = true
	cs.cancel()
}

// deliver queues msg for every member of cs, without waiting. It is called on the
// driver's goroutine, which reads every reply of the connection: a member whose
// queue is full, or whose waiting payloads would pass the byte bound, is ended
// with ErrSubscriberTooSlow instead. A message larger than the byte bound is
// still queued for a member with nothing waiting, so a bound smaller than one
// message slows nobody down for good.
func (ps *pubsub) deliver(cs *channelSubscription, msg valkey.PubSubMessage) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	size := int64(len(msg.Message))
	for sub := range cs.members {
		if pending := sub.pending.Add(size); pending > size && pending > ps.backlogBytes {
			sub.pending.Add(-size)
			ps.end(cs, sub, ErrSubscriberTooSlow)

			continue
		}

		select {
		case sub.queue <- msg:
		default:
			sub.pending.Add(-size)
			ps.end(cs, sub, ErrSubscriberTooSlow)
		}
	}
}

// confirm records the server confirming cs's subscription. A confirmation after
// the first is the driver subscribing again on a new connection after losing the
// one carrying it: whatever was published in between is lost, so the members are
// told rather than resumed in silence (ErrSubscriptionInterrupted).
func (ps *pubsub) confirm(cs *channelSubscription) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if !cs.isConfirmed {
		cs.isConfirmed = true
		close(cs.confirmed)

		return
	}

	for sub := range cs.members {
		ps.end(cs, sub, ErrSubscriptionInterrupted)
	}
}

// finish records the end of cs's server subscription, err being what ended it:
// every member still subscribed is ended, with err itself when the server never
// confirmed the subscription and with ErrSubscriptionInterrupted otherwise, and a
// Subscribe of the channel is free to open a new one.
func (ps *pubsub) finish(cs *channelSubscription, err error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	reason := err
	switch {
	case cs.isConfirmed && err != nil:
		reason = fmt.Errorf("%w: %w", ErrSubscriptionInterrupted, err)
	case cs.isConfirmed, err == nil:
		reason = ErrSubscriptionInterrupted
	}

	for sub := range cs.members {
		ps.end(cs, sub, reason)
	}

	cs.closing = true
	if ps.channels[cs.channel] == cs {
		delete(ps.channels, cs.channel)
	}

	close(cs.done)
}

// serve holds cs's server subscription until receiveCtx ends, or until it ends on
// its own.
//
// The driver re-subscribes on a new connection after losing the one a
// subscription was on, and says nothing to the caller: the return hook stops it,
// by ending receiveCtx when the subscription ended without receiveCtx having
// ended first, so the members are told instead. When receiveCtx did end first,
// the last member left, and the hook unsubscribes on the connection holding the
// subscription, since the driver leaves a subscription on the server when the
// Receive that opened it returns.
func (ps *pubsub) serve(receiveCtx context.Context, conn *connection, cs *channelSubscription) {
	hookCtx := valkey.WithOnSubscriptionHook(receiveCtx, func(s valkey.PubSubSubscription) {
		if s.Kind == "subscribe" {
			ps.confirm(cs)
		}
	})

	hookCtx = valkey.WithOnReceiveReturnHook(hookCtx, func(err error, client valkey.CommandClient) error {
		if receiveCtx.Err() == nil {
			cs.cancel()

			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), unsubscribeTimeout)
		defer cancel()

		_ = client.Do(ctx, client.B().Unsubscribe().Channel(cs.channel).Build()).Error()

		return err
	})

	err := conn.client.Receive(hookCtx, conn.client.B().Subscribe().Channel(cs.channel).Build(), func(msg valkey.PubSubMessage) {
		ps.deliver(cs, msg)
	})

	// Ended by the last member leaving: not a failure of anyone's.
	if receiveCtx.Err() != nil && ps.released(cs) {
		err = nil
	}

	cs.cancel()
	ps.finish(cs, err)
}

// released reports that cs was ended because its last member left.
func (ps *pubsub) released(cs *channelSubscription) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	return cs.closing && len(cs.members) == 0
}
