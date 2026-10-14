package localproxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingWriter struct {
	header   http.Header
	mu       sync.Mutex
	body     bytes.Buffer
	writes   []int
	flushed  chan struct{}
	writeErr error
}

func (w *recordingWriter) Header() http.Header { return w.header }
func (w *recordingWriter) WriteHeader(int)     {}
func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	w.writes = append(w.writes, len(p))
	return w.body.Write(p)
}
func (w *recordingWriter) Flush() {
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}
func (w *recordingWriter) snapshot() ([]byte, []int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.body.Bytes()), append([]int(nil), w.writes...)
}
func testResponseBuffer(t *testing.T, header http.Header) (*responseBuffer, *recordingWriter) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dst := &recordingWriter{header: header, flushed: make(chan struct{}, 1)}
	w := &responseBuffer{ResponseWriter: dst, method: http.MethodGet, ctx: ctx, cancel: cancel}
	t.Cleanup(func() { w.abort(); cancel() })
	w.WriteHeader(http.StatusOK)
	return w, dst
}

func TestOrdinaryJSONBufferPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, method, mediaType, length, trailer, buffering string
		status                                              int
		want                                                bool
	}{
		{name: "chunked-json", mediaType: "application/json; charset=utf-8", want: true},
		{name: "large-json", mediaType: "application/json", length: "65536", want: true},
		{name: "json-suffix", mediaType: "application/ld+json", want: true},
		{name: "small-json", mediaType: "application/json", length: "100"},
		{name: "empty-json", mediaType: "application/json", length: "0"},
		{name: "sse", mediaType: "text/event-stream"},
		{name: "ndjson", mediaType: "application/x-ndjson"},
		{name: "json-sequence", mediaType: "application/json-seq"},
		{name: "binary", mediaType: "application/octet-stream"},
		{name: "missing-type"},
		{name: "invalid-type", mediaType: "application/json; broken"},
		{name: "head", mediaType: "application/json", method: http.MethodHead},
		{name: "upgrade", mediaType: "application/json", status: http.StatusSwitchingProtocols},
		{name: "no-content", mediaType: "application/json", status: http.StatusNoContent},
		{name: "not-modified", mediaType: "application/json", status: http.StatusNotModified},
		{name: "range", mediaType: "application/json", status: http.StatusPartialContent},
		{name: "trailer", mediaType: "application/json", trailer: "X-Checksum"},
		{name: "explicit-stream", mediaType: "application/json", buffering: "no"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := make(http.Header)
			header.Set("Content-Type", tc.mediaType)
			if tc.length != "" {
				header.Set("Content-Length", tc.length)
			}
			if tc.trailer != "" {
				header.Set("Trailer", tc.trailer)
			}
			if tc.buffering != "" {
				header.Set("X-Accel-Buffering", tc.buffering)
			}
			method, status := tc.method, tc.status
			if method == "" {
				method = http.MethodGet
			}
			if status == 0 {
				status = http.StatusOK
			}
			if got := shouldBufferJSON(method, status, header); got != tc.want {
				t.Fatalf("buffer policy = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResponseBufferCoalescesFragmentedJSON(t *testing.T) {
	w, dst := testResponseBuffer(t, http.Header{"Content-Type": {"application/json"}})
	payload := []byte(`{"data":"` + strings.Repeat("x", 256*1024) + `"}`)
	chunks := 0
	for offset := 0; offset < len(payload); {
		end := min(offset+256, len(payload))
		if _, err := w.Write(payload[offset:end]); err != nil {
			t.Fatal(err)
		}
		// ReverseProxy calls Flush after every write for an unknown-length body.
		if err := w.FlushError(); err != nil {
			t.Fatal(err)
		}
		offset = end
		chunks++
	}
	if err := w.finish(); err != nil {
		t.Fatal(err)
	}
	body, writes := dst.snapshot()
	if !bytes.Equal(body, payload) {
		t.Fatal("buffering changed response bytes")
	}
	if len(writes) >= chunks/10 {
		t.Fatalf("only coalesced %d input chunks into %d writes", chunks, len(writes))
	}
	if w.buffer.Size() != responseBufferSize {
		t.Fatal("response buffer size grew with payload")
	}
	t.Logf("%d fragmented input chunks became %d underlying writes", chunks, len(writes))
}

func TestResponseBufferFlushesPartialDataWhileUpstreamIsIdle(t *testing.T) {
	w, dst := testResponseBuffer(t, http.Header{"Content-Type": {"application/json"}})
	if _, err := w.Write([]byte(`{"data":`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dst.flushed:
	case <-time.After(time.Second):
		t.Fatal("partial JSON stayed buffered while upstream was idle")
	}
	body, _ := dst.snapshot()
	if string(body) != `{"data":` {
		t.Fatal("timer did not flush partial JSON")
	}
}

func TestResponseBufferAbortDiscardsPendingBytesAndStopsTimer(t *testing.T) {
	w, dst := testResponseBuffer(t, http.Header{"Content-Type": {"application/json"}})
	_, _ = w.Write([]byte("unfinished"))
	w.abort()
	time.Sleep(2 * responseBufferDelay)
	body, _ := dst.snapshot()
	if len(body) != 0 {
		t.Fatal("aborted response was written after handler returned")
	}
}

func TestResponseBufferFlushFailureCancelsUpstream(t *testing.T) {
	w, dst := testResponseBuffer(t, http.Header{"Content-Type": {"application/json"}})
	dst.writeErr = io.ErrClosedPipe
	_, _ = w.Write([]byte("pending"))
	select {
	case <-w.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("failed asynchronous write did not cancel upstream")
	}
	if err := w.finish(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("finish error = %v", err)
	}
}

func TestResponseBufferCancellationDoesNotFlushPendingBytes(t *testing.T) {
	w, dst := testResponseBuffer(t, http.Header{"Content-Type": {"application/json"}})
	_, _ = w.Write([]byte("pending"))
	w.cancel()
	if err := w.finish(); !errors.Is(err, context.Canceled) {
		t.Fatalf("finish error = %v", err)
	}
	body, _ := dst.snapshot()
	if len(body) != 0 {
		t.Fatal("cancelled response flushed pending bytes")
	}
}

func bufferedTestProxy(t *testing.T, handler http.HandlerFunc) (string, *http.Client) {
	t.Helper()
	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)
	p, err := Start(backend.URL, &fakeIssuer{}, false, false, testSession("initial-session"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	ip, port := p.Backend()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableCompression: true}}
	t.Cleanup(client.CloseIdleConnections)
	return "http://" + net.JoinHostPort(ip, strconv.Itoa(port)), client
}

func TestProxyBufferedJSONIsByteIdenticalForFixedAndChunkedBodies(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("fixed-length=%v", fixed), func(t *testing.T) {
			payload := []byte(`{"data":"` + strings.Repeat("x", 256*1024) + `"}`)
			proxyURL, client := bufferedTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("ETag", `"unchanged"`)
				if fixed {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
				}
				for offset := 0; offset < len(payload); offset += 512 {
					_, _ = w.Write(payload[offset:min(offset+512, len(payload))])
					w.(http.Flusher).Flush()
				}
			})
			resp, err := client.Get(proxyURL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || !bytes.Equal(body, payload) {
				t.Fatalf("body was altered or truncated: %v", err)
			}
			if resp.Header.Get("ETag") != `"unchanged"` || len(resp.Cookies()) != 1 {
				t.Fatal("response headers or bootstrap cookie changed")
			}
			if fixed && resp.ContentLength != int64(len(payload)) {
				t.Fatal("Content-Length changed")
			}
		})
	}
}

func TestProxyStreamsFirstEventBeforeUpstreamFinishes(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "application/x-ndjson"} {
		t.Run(contentType, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			proxyURL, client := bufferedTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, "data: first\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			resp, err := client.Get(proxyURL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			first := make([]byte, len("data: first\n\n"))
			if _, err := io.ReadFull(resp.Body, first); err != nil {
				t.Fatal(err)
			}
			if string(first) != "data: first\n\n" {
				t.Fatal("first streaming event changed")
			}
			once.Do(func() { close(release) })
		})
	}
}

func TestProxyUpgradeStillHijacksConnection(t *testing.T) {
	proxyURL, _ := bufferedTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	})
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyURL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nCookie: penguin_session=valid-session\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d", resp.StatusCode)
	}
	_, _ = conn.Write([]byte("upgrade-echo"))
	got := make([]byte, len("upgrade-echo"))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "upgrade-echo" {
		t.Fatal("upgraded connection was altered")
	}
}

func TestProxyTruncatedJSONDoesNotBecomeSuccessfulResponse(t *testing.T) {
	proxyURL, client := bufferedTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(2*responseBufferSize))
		_, _ = io.WriteString(w, `{"unfinished":`)
	})
	resp, err := client.Get(proxyURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("truncated JSON became a complete HTTP response")
	}
}

func TestProxyCancellationReachesUpstream(t *testing.T) {
	cancelled := make(chan struct{})
	proxyURL, client := bufferedTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"pending":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, proxyURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not reach upstream")
	}
}

func TestProxyPreservesTrailers(t *testing.T) {
	proxyURL, client := bufferedTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Trailer", "X-Checksum")
		_, _ = io.WriteString(w, `{"ok":true}`)
		w.Header().Set("X-Checksum", "fixture-checksum")
	})
	resp, err := client.Get(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || string(data) != `{"ok":true}` || resp.Trailer.Get("X-Checksum") != "fixture-checksum" {
		t.Fatalf("response trailers changed: %v", err)
	}
}
