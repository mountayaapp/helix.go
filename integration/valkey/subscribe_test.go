package valkey

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awaitSubscribed waits until the server has confirmed channel's subscription, the
// moment from which a message published to it is delivered.
func awaitSubscribed(t *testing.T, conn *connection, channel string) {
	t.Helper()

	require.Eventually(t, func() bool {
		conn.pubsub.mu.Lock()
		defer conn.pubsub.mu.Unlock()

		cs := conn.pubsub.channels[channel]

		return cs != nil && cs.isConfirmed && !cs.closing
	}, 5*time.Second, 5*time.Millisecond, "%s was never subscribed", channel)
}

// serverSubscribers answers how many connections the server holds subscribed to
// channel.
func serverSubscribers(t *testing.T, conn *connection, channel string) int64 {
	t.Helper()

	counts, err := conn.Do(t.Context(), conn.B().PubsubNumsub().Channel(channel).Build()).AsIntMap()
	require.NoError(t, err)

	return counts[channel]
}

// subscribeInBackground runs Subscribe in a goroutine and answers the channel its
// result arrives on.
func subscribeInBackground(ctx context.Context, conn *connection, channel string, handler func(context.Context, PubSubMessage)) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- conn.Subscribe(ctx, channel, handler)
	}()

	return done
}

// awaitResult waits for a Subscribe's result.
func awaitResult(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Subscribe did not return")
		return nil
	}
}

// TestValkey_SubscribeIsolatesASlowSubscriber pins that a handler which stops
// returning holds up nobody else on the connection: another channel's subscriber
// keeps receiving and a command keeps being answered, while the slow subscriber is
// ended with ErrSubscriberTooSlow once it is further behind than its backlog. It
// used to hold up the driver's reader, which every subscription and every command
// reply of the connection shares.
//
// Ending it cancels the ctx its handler was called with, so a handler waiting on
// that ctx returns at once; one that ignores it returns when it is done waiting,
// and Subscribe returns then.
func TestValkey_SubscribeIsolatesASlowSubscriber(t *testing.T) {
	for name, waitsOnItsCtx := range map[string]bool{
		"a handler waiting on its ctx": true,
		"a handler ignoring its ctx":   false,
	} {
		t.Run(name, func(t *testing.T) {
			conn, _ := connectTest(t, Config{SubscriberBacklog: 8})
			ctx := t.Context()

			release := make(chan struct{})
			slow := subscribeInBackground(ctx, conn, "events:slow", func(ctx context.Context, _ PubSubMessage) {
				if waitsOnItsCtx {
					select {
					case <-release:
					case <-ctx.Done():
					}

					return
				}

				<-release
			})

			received := make(chan string, 16)
			fastCtx, stopFast := context.WithCancel(ctx)
			defer stopFast()
			fast := subscribeInBackground(fastCtx, conn, "events:fast", func(_ context.Context, msg PubSubMessage) {
				received <- string(msg.Payload)
			})

			awaitSubscribed(t, conn, "events:slow")
			awaitSubscribed(t, conn, "events:fast")

			for range 64 {
				require.NoError(t, conn.Publish(ctx, "events:slow", []byte("backlog")))
			}

			// While the slow handler still holds its message.
			require.NoError(t, conn.Publish(ctx, "events:fast", []byte("still delivered")))
			select {
			case got := <-received:
				assert.Equal(t, "still delivered", got)
			case <-time.After(2 * time.Second):
				require.FailNow(t, "the other channel's subscriber stopped receiving")
			}

			commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			require.NoError(t, conn.Set(commandCtx, "events:key", []byte("v"), nil), "a command was held up behind the slow subscriber")

			if !waitsOnItsCtx {
				close(release)
			}
			assert.ErrorIs(t, awaitResult(t, slow), ErrSubscriberTooSlow)
			if waitsOnItsCtx {
				close(release)
			}

			stopFast()
			assert.NoError(t, awaitResult(t, fast))
		})
	}
}

// TestValkey_SubscribeBoundsTheBacklogInBytes pins the byte bound beside the
// message bound: large messages end a subscriber that is not handling them long
// before the message count does, while a single message larger than the bound is
// still delivered to a subscriber with nothing waiting.
func TestValkey_SubscribeBoundsTheBacklogInBytes(t *testing.T) {
	conn, _ := connectTest(t, Config{SubscriberBacklogBytes: 1024})
	ctx := t.Context()

	t.Run("a subscriber behind by more bytes than the bound is ended", func(t *testing.T) {
		done := subscribeInBackground(ctx, conn, "events:large", func(ctx context.Context, _ PubSubMessage) {
			<-ctx.Done()
		})
		awaitSubscribed(t, conn, "events:large")

		for range 4 {
			require.NoError(t, conn.Publish(ctx, "events:large", bytes.Repeat([]byte("x"), 600)))
		}

		assert.ErrorIs(t, awaitResult(t, done), ErrSubscriberTooSlow)
	})

	t.Run("one message over the bound reaches an idle subscriber", func(t *testing.T) {
		received := make(chan int, 1)
		subCtx, cancel := context.WithCancel(ctx)

		done := subscribeInBackground(subCtx, conn, "events:oversized", func(_ context.Context, msg PubSubMessage) {
			received <- len(msg.Payload)
		})
		awaitSubscribed(t, conn, "events:oversized")

		require.NoError(t, conn.Publish(ctx, "events:oversized", bytes.Repeat([]byte("x"), 4096)))
		select {
		case n := <-received:
			assert.Equal(t, 4096, n)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "the oversized message was not delivered")
		}

		cancel()
		assert.NoError(t, awaitResult(t, done))
	})
}

// TestValkey_SubscribeSharesOneServerSubscription pins the reference count: two
// Subscribes of one channel hold one server subscription, the first to leave does
// not cut the other off, and the last to leave unsubscribes, where a Receive that
// returned used to leave its subscription on the server until the connection
// dropped.
func TestValkey_SubscribeSharesOneServerSubscription(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	ctx := t.Context()

	firstCtx, leaveFirst := context.WithCancel(ctx)
	first := subscribeInBackground(firstCtx, conn, "events:shared", func(context.Context, PubSubMessage) {})

	received := make(chan string, 4)
	secondCtx, leaveSecond := context.WithCancel(ctx)
	second := subscribeInBackground(secondCtx, conn, "events:shared", func(_ context.Context, msg PubSubMessage) {
		received <- string(msg.Payload)
	})

	awaitSubscribed(t, conn, "events:shared")
	require.Eventually(t, func() bool {
		conn.pubsub.mu.Lock()
		defer conn.pubsub.mu.Unlock()

		return len(conn.pubsub.channels["events:shared"].members) == 2
	}, 5*time.Second, 5*time.Millisecond)

	assert.Equal(t, int64(1), serverSubscribers(t, conn, "events:shared"), "two local subscribers, one server subscription")

	leaveFirst()
	require.NoError(t, awaitResult(t, first))
	assert.Equal(t, int64(1), serverSubscribers(t, conn, "events:shared"), "the remaining subscriber keeps the server subscription")

	require.NoError(t, conn.Publish(ctx, "events:shared", []byte("after the first left")))
	select {
	case got := <-received:
		assert.Equal(t, "after the first left", got)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the remaining subscriber was cut off")
	}

	leaveSecond()
	require.NoError(t, awaitResult(t, second))
	require.Eventually(t, func() bool {
		return serverSubscribers(t, conn, "events:shared") == 0
	}, 5*time.Second, 10*time.Millisecond, "the last subscriber to leave unsubscribes")
}

// TestValkey_SubscribeRejoinsAChannelBeingEnded pins the race between the last
// subscriber leaving and a new one arriving: the UNSUBSCRIBE of the server
// subscription being ended never cuts off the one opened after it.
func TestValkey_SubscribeRejoinsAChannelBeingEnded(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	ctx := t.Context()

	for range 20 {
		subCtx, cancel := context.WithCancel(ctx)
		done := subscribeInBackground(subCtx, conn, "events:churn", func(context.Context, PubSubMessage) {})
		cancel()
		require.NoError(t, awaitResult(t, done))
	}

	received := make(chan struct{}, 64)
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := subscribeInBackground(subCtx, conn, "events:churn", func(context.Context, PubSubMessage) {
		received <- struct{}{}
	})
	awaitSubscribed(t, conn, "events:churn")

	// The subscriber stays subscribed through every teardown before it.
	require.Never(t, func() bool {
		return serverSubscribers(t, conn, "events:churn") != 1
	}, 300*time.Millisecond, 20*time.Millisecond)

	require.NoError(t, conn.Publish(ctx, "events:churn", []byte("delivered")))
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the subscriber opened after the teardowns was cut off")
	}

	cancel()
	assert.NoError(t, awaitResult(t, done))
}

// TestValkey_SubscribeReportsALostConnection pins that a subscription whose
// connection is lost is ended with ErrSubscriptionInterrupted rather than resumed
// in silence on a new one: what was published in between was lost, and a caller
// told of it can read it back from a durable source. The driver resubscribes by
// itself, which used to leave the caller receiving again with no word of the gap.
func TestValkey_SubscribeReportsALostConnection(t *testing.T) {
	conn, _ := connectTest(t, Config{})
	ctx := t.Context()

	done := subscribeInBackground(ctx, conn, "events:lost", func(context.Context, PubSubMessage) {})
	awaitSubscribed(t, conn, "events:lost")

	// The command travels on the connection it kills, so its own answer may be
	// the connection failing.
	_ = conn.Do(ctx, conn.B().ClientKill().TypePubsub().SkipmeNo().Build()).Error()

	err := awaitResult(t, done)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSubscriptionInterrupted)

	require.Never(t, func() bool {
		return serverSubscribers(t, conn, "events:lost") != 0
	}, 1500*time.Millisecond, 50*time.Millisecond, "the driver subscribed again in silence")

	// The channel can be subscribed again, and delivers.
	received := make(chan struct{}, 1)
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	again := subscribeInBackground(subCtx, conn, "events:lost", func(context.Context, PubSubMessage) {
		received <- struct{}{}
	})
	awaitSubscribed(t, conn, "events:lost")

	require.NoError(t, conn.Publish(ctx, "events:lost", []byte("after")))
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "a subscription opened after the loss does not deliver")
	}

	cancel()
	assert.NoError(t, awaitResult(t, again))
}
