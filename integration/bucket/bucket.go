package bucket

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service"
	"github.com/mountaya/helix.go/telemetry/trace"

	"gocloud.dev/blob"
)

// Bucket exposes an opinionated and standardized way to interact with buckets across
// different providers through drivers.
//
// Every method is traced and measured as one operation, named after the method. A
// missing blob is reported as an error wrapping ErrNotFound and is not recorded as
// an error on the operation: see IsNotFound.
//
// A Bucket is safe for concurrent use. The readers and writers it opens are not.
type Bucket interface {

	// Exists reports whether a blob exists at key. A missing blob is false with a
	// nil error. An error means the question could not be answered — an
	// unreachable bucket is not an absent blob — and is returned as such.
	Exists(ctx context.Context, key string) (bool, error)

	// Read returns the content of the blob at key, read whole into memory. Use
	// NewReader for a blob too large for that.
	//
	// Returns an error wrapping ErrNotFound when there is no blob at key.
	Read(ctx context.Context, key string) ([]byte, error)

	// NewReader opens the blob at key for reading as a stream.
	//
	// The caller must Close the reader. The operation lasts until then, so that its
	// span and its duration cover the whole transfer: a reader never closed is an
	// operation never recorded. ctx applies to every read for as long as the
	// reader is open.
	//
	// Returns an error wrapping ErrNotFound when there is no blob at key.
	NewReader(ctx context.Context, key string) (io.ReadCloser, error)

	// Write writes value as the blob at key, with the optional opts, replacing any
	// blob already there.
	Write(ctx context.Context, key string, value []byte, opts *OptionsWrite) error

	// NewWriter opens the blob at key for writing as a stream, with the optional
	// opts. The blob replaces any already at key only once Close returns nil:
	// until then readers still see the previous one, and an error from Close means
	// nothing was written.
	//
	// The caller must Close the writer, even to abandon the write — cancel ctx
	// first, so that the write is abandoned rather than committed. The operation
	// lasts until Close, as for NewReader. ctx applies to every write for as long
	// as the writer is open.
	NewWriter(ctx context.Context, key string, opts *OptionsWrite) (io.WriteCloser, error)

	// Delete deletes the blob at key.
	//
	// Returns an error wrapping ErrNotFound when there is no blob at key.
	Delete(ctx context.Context, key string) error
}

// openBucket opens the bucket a valid Config names through its driver, along with
// what the connection must close after it. It is a variable so that a test can
// observe the bucket a failed Connect opened, which nothing else hands back.
var openBucket = func(ctx context.Context, cfg *Config) (*blob.Bucket, io.Closer, error) {
	return cfg.Driver.open(ctx, cfg.Bucket)
}

// connection is the Bucket handed to the caller. Its lifecycle is reachable only
// through dependency, which is what the Service holds.
type connection struct {

	// inst names this connection to the telemetry pipeline. Every span below is
	// opened through it, which is what keeps the naming the same here as in the
	// eight other integrations.
	inst *integration.Instrument

	// client is the connection made with the Bucket client.
	client *blob.Bucket

	// owned is what the driver opened beside the bucket and the connection closes
	// after it — the Cloud Storage client — or nil when there is nothing.
	owned io.Closer

	// closeOnce makes close idempotent. The driver reports a second Close as an
	// error, and a bucket closed by a failed Connect must not then fail the
	// Service's own shutdown.
	closeOnce sync.Once
}

// Connect opens the bucket cfg describes, checks that it can be reached, and
// attaches it to svc, which owns its lifecycle from then on.
//
// Returns a validation error, before anything is opened, if cfg is not valid. A
// bucket the driver cannot open, or cannot list within ctx — a missing bucket,
// credentials that are refused, an endpoint that does not answer — is an error
// coded SERVICE_UNAVAILABLE, whose "reason" extension carries the driver's
// message: the service fails at start-up rather than at its first request.
func Connect(ctx context.Context, svc *service.Service, cfg Config) (Bucket, error) {

	// No need to continue if Config is not valid.
	if err := cfg.sanitize(); err != nil {
		return nil, err
	}

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier:     identifier,
		Humanized:      humanized,
		Name:           cfg.name(),
		Attributes:     instrumentAttributes(&cfg),
		TracerProvider: service.TracerProvider(svc),
		MeterProvider:  service.MeterProvider(svc),
		Propagator:     service.Propagator(svc),
	})
	if err != nil {
		return nil, err
	}

	client, owned, err := open(ctx, &cfg)
	if err != nil {
		return nil, errorstack.Wrap(err, "Failed to open bucket",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	conn := &connection{inst: inst, client: client, owned: owned}

	// Opening a bucket makes no request for most drivers, so this is the first
	// moment a wrong name or a refused credential can show.
	if err := accessible(ctx, client); err != nil {
		_ = conn.close()

		return nil, errorstack.Wrap(err, "Failed to reach bucket",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	// Hand the connection to the Service, which owns its lifecycle from here. A
	// Service that refuses it owns nothing, so the bucket is closed here.
	if err := service.Attach(svc, cfg.name(), dependency{conn: conn}); err != nil {
		_ = conn.close()
		return nil, err
	}

	return conn, nil
}

// open opens the bucket through the driver cfg names, inside Subfolder when it
// is set, along with what the connection must close after it.
func open(ctx context.Context, cfg *Config) (*blob.Bucket, io.Closer, error) {
	client, owned, err := openBucket(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	if cfg.Subfolder != "" {
		client = blob.PrefixedBucket(client, cfg.Subfolder)
	}

	return client, owned, nil
}

// accessible returns nil when the bucket can be listed, and why not otherwise.
func accessible(ctx context.Context, client *blob.Bucket) error {
	up, err := client.IsAccessible(ctx)
	switch {
	case err != nil:
		return err
	case !up:
		return errNotAccessible
	default:
		return nil
	}
}

// close closes the bucket, then what the driver opened beside it. Only the first
// call reaches the driver; a later one returns nil.
func (conn *connection) close() error {
	var err error
	conn.closeOnce.Do(func() {
		err = conn.client.Close()
		if conn.owned != nil {
			err = errors.Join(err, conn.owned.Close())
		}
	})

	return err
}

// Exists reports whether a blob exists at key.
//
// The error is returned rather than discarded: an unreachable bucket and an
// absent object are not the same answer, and a caller told only "false" cannot
// tell them apart — nor could a trace, since the failure was not recorded either.
//
// It automatically handles tracing and error recording.
func (conn *connection) Exists(ctx context.Context, key string) (bool, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Exists")
	defer op.End(ctx)

	op.SetAttributes(attrKeyKey.String(key))

	exists, err := conn.client.Exists(ctx, key)
	if err != nil {
		op.RecordError("failed to check blob existence", err)
	}

	return exists, err
}

// Read reads the blob at key and returns its byte representation.
//
// It automatically handles tracing and error recording.
func (conn *connection) Read(ctx context.Context, key string) ([]byte, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Read")
	defer op.End(ctx)

	op.SetAttributes(attrKeyKey.String(key))

	value, err := conn.client.ReadAll(ctx, key)
	if missing := asNotFound(err); missing != nil {
		return nil, missing
	}

	if err != nil {
		op.RecordError("failed to read blob", err)
	}

	return value, err
}

// NewReader opens the blob at key for reading as a stream.
//
// It automatically handles tracing and error recording, until the reader is
// closed.
func (conn *connection) NewReader(ctx context.Context, key string) (io.ReadCloser, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "NewReader")
	op.SetAttributes(attrKeyKey.String(key))

	r, err := conn.client.NewReader(ctx, key, nil)
	if missing := asNotFound(err); missing != nil {
		op.End(ctx)
		return nil, missing
	}

	if err != nil {
		op.RecordError("failed to read blob", err)
		op.End(ctx)

		return nil, err
	}

	return &reader{r: r, ctx: ctx, op: op}, nil
}

// Write writes bytes representation of blob at key, with some optional options.
//
// It automatically handles tracing and error recording.
func (conn *connection) Write(ctx context.Context, key string, value []byte, opts *OptionsWrite) error {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Write")
	defer op.End(ctx)

	op.SetAttributes(attrKeyKey.String(key))

	err := conn.client.WriteAll(ctx, key, value, opts.writerOptions())
	if err != nil {
		op.RecordError("failed to write blob", err)
	}

	return err
}

// NewWriter opens the blob at key for writing as a stream.
//
// It automatically handles tracing and error recording, until the writer is
// closed.
func (conn *connection) NewWriter(ctx context.Context, key string, opts *OptionsWrite) (io.WriteCloser, error) {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "NewWriter")
	op.SetAttributes(attrKeyKey.String(key))

	w, err := conn.client.NewWriter(ctx, key, opts.writerOptions())
	if err != nil {
		op.RecordError("failed to write blob", err)
		op.End(ctx)

		return nil, err
	}

	return &writer{w: w, ctx: ctx, op: op}, nil
}

// Delete deletes existing blob at key.
//
// It automatically handles tracing and error recording.
func (conn *connection) Delete(ctx context.Context, key string) error {
	ctx, op := conn.inst.StartOperation(ctx, trace.SpanKindClient, "Delete")
	defer op.End(ctx)

	op.SetAttributes(attrKeyKey.String(key))

	err := conn.client.Delete(ctx, key)
	if missing := asNotFound(err); missing != nil {
		return missing
	}

	if err != nil {
		op.RecordError("failed to delete blob", err)
	}

	return err
}
