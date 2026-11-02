package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// idleCloser is a RoundTripper that records whether its idle connections were
// closed.
type idleCloser struct {
	http.RoundTripper

	closed bool
}

func (c *idleCloser) CloseIdleConnections() { c.closed = true }

// TestRoundTripper_ForwardsCloseIdleConnections pins that wrapping a transport
// does not hide its pool: http.Client.CloseIdleConnections finds the method by
// type assertion on the outermost transport.
func TestRoundTripper_ForwardsCloseIdleConnections(t *testing.T) {
	base := &idleCloser{}
	client := &http.Client{Transport: mustInstrument(t, InstrumentConfig{
		Identifier: "httpclient",
		Humanized:  "HTTP Client",
	}).NewHTTPTransport().RoundTripper(base)}

	client.CloseIdleConnections()

	assert.True(t, base.closed)
}
