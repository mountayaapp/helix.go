package httpclient

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawEndpoint starts a listener that answers every request with response, written
// as it is, and returns its URL. It is how a test meets a response net/http cannot
// frame, which no http.Handler can write.
func rawEndpoint(t *testing.T, response string) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil || line == "\r\n" {
						break
					}
				}

				_, _ = conn.Write([]byte(response))
			}()
		}
	}()

	return "http://" + ln.Addr().String()
}

// TestRedact_LeavesOutAnAnswerNetHTTPCouldNotFrame pins that a response net/http
// refuses to read, its status line or a header line malformed, leaves nothing of
// what the endpoint wrote in the error: the standard library quotes the line or
// the value it refused, which can repeat the request's query or hold whatever the
// endpoint put there. What it was is kept, as the sentence that names it.
func TestRedact_LeavesOutAnAnswerNetHTTPCouldNotFrame(t *testing.T) {
	const secret = "S3CRETKEY0123"

	testcases := []struct {
		name     string
		response string
		says     string
	}{
		{
			name:     "a control byte in a header line",
			response: "HTTP/1.1 302 Found\r\nLocation: http://example.com/next?key=" + secret + "\x01\r\nContent-Length: 0\r\n\r\n",
			says:     "malformed MIME header line",
		},
		{
			name:     "a header line without a colon",
			response: "HTTP/1.1 200 OK\r\n" + secret + "\r\nContent-Length: 0\r\n\r\n",
			says:     "malformed MIME header: missing colon",
		},
		{
			name:     "a continuation line first",
			response: "HTTP/1.1 200 OK\r\n " + secret + "\r\nContent-Length: 0\r\n\r\n",
			says:     "malformed MIME header initial line",
		},
		{
			name:     "a status line that is none",
			response: secret + "\r\n\r\n",
			says:     "malformed HTTP response",
		},
		{
			name:     "a status code that is none",
			response: "HTTP/1.1 " + secret + " OK\r\nContent-Length: 0\r\n\r\n",
			says:     "malformed HTTP status code",
		},
		{
			name:     "a version that is none",
			response: "HTTP/" + secret + " 200 OK\r\nContent-Length: 0\r\n\r\n",
			says:     "malformed HTTP version",
		},
		{
			name:     "a Content-Length that is no number",
			response: "HTTP/1.1 200 OK\r\nContent-Length: " + secret + "\r\n\r\n",
			says:     "bad Content-Length",
		},
		{
			name:     "two Content-Lengths that differ",
			response: "HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: " + secret + "\r\n\r\n",
			says:     "http: message cannot contain multiple Content-Length headers",
		},
		{
			name:     "a transfer encoding net/http does not read",
			response: "HTTP/1.1 200 OK\r\nTransfer-Encoding: " + secret + "\r\n\r\n",
			says:     "unsupported transfer encoding",
		},
		{
			name:     "two transfer encodings",
			response: "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: " + secret + "\r\n\r\n",
			says:     "too many transfer encodings",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := (&http.Client{}).Get(rawEndpoint(t, tc.response) + "/path")
			if resp != nil {
				_ = resp.Body.Close()
			}

			require.Error(t, err)
			require.Contains(t, err.Error(), secret, "net/http did not quote the endpoint: the case proves nothing")

			redacted := redact(err).Error()

			assert.NotContains(t, redacted, secret)
			assert.True(t, strings.HasSuffix(redacted, tc.says), "the error reads %q, want it to end with %q", redacted, tc.says)
		})
	}
}

// TestWithoutAnswer pins the sentences after which the standard library quotes
// what an endpoint answered, the HTTP/2 transport's among them, which an HTTP/1
// endpoint cannot make it write: each is kept, and nothing after it.
func TestWithoutAnswer(t *testing.T) {
	testcases := []struct {
		text string
		want string
		ok   bool
	}{
		{text: `net/http: HTTP/1.x transport connection broken: malformed MIME header line: "Location: http://x/?k=S3CRET\x01"`, want: "net/http: HTTP/1.x transport connection broken: malformed MIME header line", ok: true},
		{text: `stream error: stream ID 1; PROTOCOL_ERROR; invalid header field name "S3CRET"`, want: "stream error: stream ID 1; PROTOCOL_ERROR; invalid header field name", ok: true},
		{text: `stream error: stream ID 3; PROTOCOL_ERROR; invalid pseudo-header ":S3CRET"`, want: "stream error: stream ID 3; PROTOCOL_ERROR; invalid pseudo-header", ok: true},
		{text: `stream error: stream ID 3; PROTOCOL_ERROR; duplicate pseudo-header ":status"`, want: "stream error: stream ID 3; PROTOCOL_ERROR; duplicate pseudo-header", ok: true},
		{text: `net/http: HTTP/1.x transport connection broken: bad trailer key "S3CRET"`, want: "net/http: HTTP/1.x transport connection broken: bad trailer key", ok: true},
		{text: `net/http: HTTP/1.x transport connection broken: invalid empty Content-Length "  "`, want: "net/http: HTTP/1.x transport connection broken: invalid empty Content-Length", ok: true},
		{text: "context deadline exceeded (Client.Timeout exceeded while awaiting headers)", ok: false},
		{text: `stream error: stream ID 1; PROTOCOL_ERROR; invalid header field value for "x-S3CRET"`, want: "stream error: stream ID 1; PROTOCOL_ERROR; invalid header field value", ok: true},
		{text: "unexpected EOF", ok: false},
	}

	for _, tc := range testcases {
		got, ok := withoutAnswer(tc.text)
		assert.Equal(t, tc.ok, ok, tc.text)
		assert.Equal(t, tc.want, got, tc.text)
	}
}
