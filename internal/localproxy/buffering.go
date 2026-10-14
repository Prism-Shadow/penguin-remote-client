package localproxy

import (
	"bufio"
	"compress/gzip"
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	responseBufferSize  = 64 * 1024
	responseBufferDelay = 25 * time.Millisecond
)

type copyBufferPool struct{ pool sync.Pool }

var responseCopyBuffers = &copyBufferPool{pool: sync.Pool{
	New: func() any { return make([]byte, responseBufferSize) },
}}

func (p *copyBufferPool) Get() []byte { return p.pool.Get().([]byte) }

func (p *copyBufferPool) Put(b []byte) {
	if cap(b) == responseBufferSize {
		p.pool.Put(b[:responseBufferSize])
	}
}

func serveBufferedResponse(proxy *httputil.ReverseProxy, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	buffered := &responseBuffer{
		ResponseWriter: w, method: r.Method, ctx: ctx, cancel: cancel,
		acceptGzip:              acceptsGzip(r.Header.Values("Accept-Encoding")),
		requestTransformBlocked: r.Header.Get("Range") != "" || hasNoTransform(r.Header),
	}
	defer buffered.abort()
	proxy.ServeHTTP(buffered, r.WithContext(ctx))
	if err := buffered.finish(); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// Go immediately flushes unknown-length responses even with FlushInterval == 0.
// Only ordinary JSON bodies coalesce those writes; upgrades and streams pass through.
type responseBuffer struct {
	http.ResponseWriter
	method                  string
	ctx                     context.Context
	cancel                  context.CancelFunc
	acceptGzip              bool
	requestTransformBlocked bool

	mu          sync.Mutex
	wroteHeader bool
	buffer      *bufio.Writer
	compressed  bool
	gzip        *gzip.Writer
	timer       *time.Timer
	closed      bool
	err         error
}

func (w *responseBuffer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseBuffer) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeHeader(status)
}

func (w *responseBuffer) writeHeader(status int) {
	if w.closed || w.wroteHeader {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true
	if shouldBufferJSON(w.method, status, w.Header()) {
		w.buffer = bufio.NewWriterSize(w.ResponseWriter, responseBufferSize)
		if w.method == http.MethodGet && !w.requestTransformBlocked &&
			w.Header().Get("Content-Encoding") == "" && w.Header().Get("Content-Range") == "" &&
			!hasNoTransform(w.Header()) {
			varyAcceptEncoding(w.Header())
			if w.acceptGzip {
				w.compressed = true
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Del("Content-Length")
				w.Header().Del("Accept-Ranges")
				stripRepresentationValidators(w.Header(), "")
			}
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func shouldBufferJSON(method string, status int, header http.Header) bool {
	if method == http.MethodHead || status != http.StatusOK ||
		header.Get("Trailer") != "" || strings.EqualFold(header.Get("X-Accel-Buffering"), "no") {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || (mediaType != "application/json" &&
		!(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json"))) {
		return false
	}
	if length, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64); err == nil &&
		length >= 0 && length < responseBufferSize {
		return false
	}
	return true
}

func (w *responseBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if !w.wroteHeader {
		w.writeHeader(http.StatusOK)
	}
	if w.buffer == nil {
		return w.ResponseWriter.Write(p)
	}
	var destination io.Writer = w.buffer
	if w.compressed {
		w.ensureGzip()
		destination = w.gzip
	}
	n, err := destination.Write(p)
	if err != nil {
		w.fail(err)
		return n, err
	}
	w.scheduleFlush()
	return n, nil
}

func (w *responseBuffer) Flush() { _ = w.FlushError() }

func (w *responseBuffer) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return io.ErrClosedPipe
	}
	if !w.wroteHeader {
		w.writeHeader(http.StatusOK)
	}
	if w.buffer == nil {
		return http.NewResponseController(w.ResponseWriter).Flush()
	}
	w.scheduleFlush()
	return nil
}

func (w *responseBuffer) scheduleFlush() {
	if w.timer == nil {
		w.timer = time.AfterFunc(responseBufferDelay, w.flushDelayed)
	}
}

func (w *responseBuffer) flushDelayed() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timer = nil
	if w.closed || w.err != nil {
		return
	}
	if err := w.ctx.Err(); err != nil {
		w.fail(err)
		return
	}
	if w.gzip != nil {
		if err := w.gzip.Flush(); err != nil {
			w.fail(err)
			return
		}
	}
	if err := w.buffer.Flush(); err != nil {
		w.fail(err)
		return
	}
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		w.fail(err)
	}
}

func (w *responseBuffer) fail(err error) {
	w.err = err
	w.cancel()
}

func (w *responseBuffer) stop() {
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

func (w *responseBuffer) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stop()
	if w.err != nil {
		return w.err
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.compressed {
		w.ensureGzip()
		if err := w.gzip.Close(); err != nil {
			return err
		}
		stripRepresentationValidators(w.Header(), http.TrailerPrefix)
	}
	if w.buffer != nil {
		if err := w.buffer.Flush(); err != nil {
			return err
		}
		// ReverseProxy installs unannounced trailers after its final Flush call.
		// Commit the buffered body before net/http can synthesize Content-Length.
		for key := range w.Header() {
			if strings.HasPrefix(key, http.TrailerPrefix) {
				return http.NewResponseController(w.ResponseWriter).Flush()
			}
		}
	}
	return nil
}

func (w *responseBuffer) abort() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stop()
	if w.gzip != nil {
		// Never close on abort: a footer would falsely complete a truncated body.
		w.gzip.Reset(io.Discard)
		gzipWriters.Put(w.gzip)
		w.gzip = nil
	}
}

func (w *responseBuffer) ensureGzip() {
	if w.gzip == nil {
		w.gzip = gzipWriters.Get().(*gzip.Writer)
		w.gzip.Reset(w.buffer)
	}
}
