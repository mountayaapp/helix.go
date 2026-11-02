package bucket

import (
	"context"
	"errors"
	"io"

	"github.com/mountayaapp/helix.go/integration"

	"gocloud.dev/blob"
)

// Ensure the streams comply to the standard library's interfaces.
var (
	_ io.ReadCloser  = (*reader)(nil)
	_ io.WriteCloser = (*writer)(nil)
)

// reader is the io.ReadCloser NewReader returns. It holds the operation open for
// as long as the blob is being read, so that the span and the duration measure
// the transfer rather than only the request that started it.
type reader struct {
	r *blob.Reader

	// ctx is the context the operation was started with. Holding a context is
	// what a stream does — the driver's reader holds the same one for its reads —
	// and ending the operation with it is what links the measurement to the span.
	ctx context.Context
	op  *integration.Operation

	// failed is set once an error has been recorded, so that a reader failing on
	// every call records one error rather than one per call.
	failed bool
	closed bool
}

// Read reads from the blob. The io.EOF that ends a blob is not a failure.
func (r *reader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.fail(err)
	}

	return n, err
}

// Close releases the reader and ends the operation. Only the first call does
// anything; a later one returns nil.
func (r *reader) Close() error {
	if r.closed {
		return nil
	}

	r.closed = true

	err := r.r.Close()
	if err != nil {
		r.fail(err)
	}

	r.op.End(r.ctx)

	return err
}

// fail records err on the operation, unless an error already was.
func (r *reader) fail(err error) {
	if r.failed {
		return
	}

	r.failed = true
	r.op.RecordError("failed to read blob", err)
}

// writer is the io.WriteCloser NewWriter returns. It holds the operation open
// until the blob is committed by Close, for the same reason as reader.
type writer struct {
	w *blob.Writer

	// ctx is the context the operation was started with, held for the reason
	// given on reader.
	ctx context.Context
	op  *integration.Operation

	// failed is set once an error has been recorded, so that a write failing and
	// the Close that follows it record one error rather than two.
	failed bool
	closed bool
}

// Write writes to the blob. A nil error does not mean the bytes are stored: only
// Close returning nil does.
func (w *writer) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if err != nil {
		w.fail(err)
	}

	return n, err
}

// Close commits the blob and ends the operation. Only the first call does
// anything; a later one returns nil.
func (w *writer) Close() error {
	if w.closed {
		return nil
	}

	w.closed = true

	err := w.w.Close()
	if err != nil {
		w.fail(err)
	}

	w.op.End(w.ctx)

	return err
}

// fail records err on the operation, unless an error already was.
func (w *writer) fail(err error) {
	if w.failed {
		return
	}

	w.failed = true
	w.op.RecordError("failed to write blob", err)
}
