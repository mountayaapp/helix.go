package valkey

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// confirmedSubscription is one SubscribeConfirmed running in a goroutine: the
// channel closed when it is confirmed, the messages its handler was handed, and
// the channel its result arrives on.
type confirmedSubscription struct {
	confirmed chan struct{}
	received  chan []byte
	done      chan error
}

// subscribeConfirmedInBackground runs SubscribeConfirmed on channel in a
// goroutine.
func subscribeConfirmedInBackground(ctx context.Context, conn *connection, channel string) confirmedSubscription {
	s := confirmedSubscription{
		confirmed: make(chan struct{}),
		received:  make(chan []byte, 16),
		done:      make(chan error, 1),
	}

	go func() {
		s.done <- conn.SubscribeConfirmed(ctx, channel, func() { close(s.confirmed) }, func(_ context.Context, msg PubSubMessage) {
			s.received <- msg.Payload
		})
	}()

	return s
}

// connectAgain connects a second client of its own to the server at address, as
// another process would be, under name.
func connectAgain(t *testing.T, address string, name string) *connection {
	t.Helper()

	svc, _ := servicetest.New(t)

	v, err := Connect(t.Context(), svc, Config{Name: name, Address: address})
	require.NoError(t, err)

	return v.(*connection)
}

// TestValkey_SubscribeConfirmed_APublishAfterTheConfirmationIsDelivered pins what
// the confirmation means: a message another client publishes once confirmed has
// been called is delivered, every time. Each trial subscribes to a channel nobody
// holds yet and publishes the moment it is told, from a client of its own, as a
// process appending to a stream and publishing it would.
//
// Told before the server confirmed the subscription, as Subscribe's own return
// would tell a caller who took it for one, the publish reaches the server first in
// most trials and the message is lost: nothing publishes it again.
func TestValkey_SubscribeConfirmed_APublishAfterTheConfirmationIsDelivered(t *testing.T) {
	address := startValkey(t)
	publisher := connectAgain(t, address, "publisher")
	subscriber := connectAgain(t, address, "subscriber")

	const trials = 200

	lost := 0
	for trial := range trials {
		channel := fmt.Sprintf("orders.%d", trial)

		ctx, cancel := context.WithCancel(t.Context())
		s := subscribeConfirmedInBackground(ctx, subscriber, channel)

		select {
		case <-s.confirmed:
		case err := <-s.done:
			cancel()
			require.FailNowf(t, "never confirmed", "trial %d: SubscribeConfirmed returned %v", trial, err)
		case <-time.After(5 * time.Second):
			cancel()
			require.FailNowf(t, "never confirmed", "trial %d", trial)
		}

		require.NoError(t, publisher.Publish(t.Context(), channel, []byte("placed")))

		select {
		case payload := <-s.received:
			assert.Equal(t, "placed", string(payload))
		case <-time.After(2 * time.Second):
			lost++
		}

		cancel()
		require.NoError(t, awaitResult(t, s.done))
	}

	assert.Zero(t, lost, "%d of %d messages published once the subscription was confirmed were never delivered", lost, trials)
}

// A SubscribeConfirmed joining a channel the connection already holds shares the
// server subscription, confirmed already, and is told at once: both subscribers
// are handed what is published next.
func TestValkey_SubscribeConfirmed_JoiningAHeldChannelIsConfirmedAtOnce(t *testing.T) {
	address := startValkey(t)
	publisher := connectAgain(t, address, "publisher")
	subscriber := connectAgain(t, address, "subscriber")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	first := subscribeConfirmedInBackground(ctx, subscriber, "orders")
	select {
	case <-first.confirmed:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the first subscription was never confirmed")
	}

	second := subscribeConfirmedInBackground(ctx, subscriber, "orders")
	select {
	case <-second.confirmed:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the second subscription was never confirmed")
	}

	assert.Equal(t, int64(1), serverSubscribers(t, subscriber, "orders"), "the two share one server subscription")

	require.NoError(t, publisher.Publish(t.Context(), "orders", []byte("placed")))

	for _, s := range []confirmedSubscription{first, second} {
		select {
		case payload := <-s.received:
			assert.Equal(t, "placed", string(payload))
		case <-time.After(2 * time.Second):
			assert.Fail(t, "a subscriber was not handed the message")
		}
	}

	cancel()
	require.NoError(t, awaitResult(t, first.done))
	require.NoError(t, awaitResult(t, second.done))
}

// TestValkey_SubscribeConfirmed_WhatACallerGetsWithoutAConfirmation pins the two
// ways the confirmation does not come, each telling the caller by
// SubscribeConfirmed's return, and confirmed never being called.
func TestValkey_SubscribeConfirmed_WhatACallerGetsWithoutAConfirmation(t *testing.T) {
	address := startValkey(t)
	admin := connectAgain(t, address, "admin")

	t.Run("ctx ends while the server holds the command: nil", func(t *testing.T) {
		subscriber := connectAgain(t, address, "subscriber")

		// Every client's commands wait while the server is paused, the
		// subscription's among them, so it cannot be confirmed before ctx ends.
		require.NoError(t, admin.Do(t.Context(), admin.B().ClientPause().Timeout(1500).All().Build()).Error())

		var called atomic.Bool

		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()

		err := subscriber.SubscribeConfirmed(ctx, "orders", func() { called.Store(true) }, func(context.Context, PubSubMessage) {})

		assert.NoError(t, err)
		assert.False(t, called.Load(), "confirmed was called for a subscription the server never confirmed")

		// Let the pause end before the next case runs.
		require.Eventually(t, func() bool {
			return admin.Do(t.Context(), admin.B().Ping().Build()).Error() == nil
		}, 5*time.Second, 50*time.Millisecond)
	})

	t.Run("the server refuses the subscription: its error", func(t *testing.T) {

		// A user the server lets run any command, on no channel.
		require.NoError(t, admin.Do(t.Context(), admin.B().AclSetuser().Username("limited").Rule("on", ">secret", "~*", "+@all", "resetchannels").Build()).Error())

		svc, _ := servicetest.New(t)
		v, err := Connect(t.Context(), svc, Config{Name: "limited", Address: address, User: "limited", Password: "secret"})
		require.NoError(t, err)

		var called atomic.Bool

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		err = v.(*connection).SubscribeConfirmed(ctx, "orders", func() { called.Store(true) }, func(context.Context, PubSubMessage) {})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "NOPERM")
		assert.NotErrorIs(t, err, ErrSubscriptionInterrupted, "a subscription never confirmed was never interrupted")
		assert.False(t, called.Load(), "confirmed was called for a subscription the server refused")
	})
}
