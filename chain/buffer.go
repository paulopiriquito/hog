package chain

import (
	"bytes"
	"net/http"
)

// Buffer is an http.ResponseWriter that captures the response body and
// status code so a response-plugin can inspect and rewrite them before they
// reach the client. Response headers are NOT buffered: NewBuffer shares the
// downstream writer's live header map, so headers set inside next.ServeHTTP
// are visible on the real writer immediately. (True header buffering is
// deferred to the response-plugin spec.) Buffer is not safe for concurrent use.
type Buffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

// NewBuffer returns a Buffer seeded from the downstream writer's live header map
// (header mutations are immediately visible on the real writer) and defaults status to 200.
func NewBuffer(w http.ResponseWriter) *Buffer {
	return &Buffer{header: w.Header(), status: http.StatusOK}
}

func (b *Buffer) Header() http.Header { return b.header }

func (b *Buffer) WriteHeader(code int) { b.status = code }

func (b *Buffer) Write(p []byte) (int, error) { return b.body.Write(p) }

// Status returns the captured status code (200 if WriteHeader was never called).
func (b *Buffer) Status() int { return b.status }

// Body returns the captured response body bytes.
func (b *Buffer) Body() []byte { return b.body.Bytes() }
