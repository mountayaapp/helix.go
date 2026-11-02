package bucket

import (
	"gocloud.dev/blob"
)

// OptionsWrite is used to set options when writing a blob, through Write or
// NewWriter.
type OptionsWrite struct {

	// CacheControl specifies caching attributes that services may use when serving
	// the blob.
	//
	// Documentation: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Cache-Control
	CacheControl string `json:"cache_control,omitempty"`

	// ContentDisposition specifies whether the blob content is expected to be
	// displayed inline or as an attachment.
	//
	// Documentation: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Disposition
	ContentDisposition string `json:"content_disposition,omitempty"`

	// ContentEncoding specifies the encoding used for the blob's content, if any.
	//
	// Documentation: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Encoding
	ContentEncoding string `json:"content_encoding,omitempty"`

	// ContentLanguage specifies the language used in the blob's content, if any.
	//
	// Documentation: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Language
	ContentLanguage string `json:"content_language,omitempty"`

	// ContentType specifies the MIME type of the blob being written. If not set,
	// it will be inferred from the content using the algorithm described at
	// https://mimesniff.spec.whatwg.org/.
	//
	// Documentation: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Type
	ContentType string `json:"content_type,omitempty"`

	// ContentMD5 is used as a message integrity check. If len(ContentMD5) > 0, the
	// MD5 hash of the bytes written must match ContentMD5.
	//
	// Documentation: https://tools.ietf.org/html/rfc1864
	ContentMD5 []byte `json:"content_md5,omitempty"`

	// Metadata holds key/value strings to be associated with the blob, or nil.
	// Keys may not be empty, and are lowercased before being written. Duplicate
	// case-insensitive keys (e.g., "foo" and "FOO") will result in an error.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// writerOptions returns opts as the driver's writer options, or nil when opts is
// nil, which the driver reads as the zero value.
func (opts *OptionsWrite) writerOptions() *blob.WriterOptions {
	if opts == nil {
		return nil
	}

	return &blob.WriterOptions{
		CacheControl:       opts.CacheControl,
		ContentDisposition: opts.ContentDisposition,
		ContentEncoding:    opts.ContentEncoding,
		ContentLanguage:    opts.ContentLanguage,
		ContentType:        opts.ContentType,
		ContentMD5:         opts.ContentMD5,
		Metadata:           opts.Metadata,
	}
}
