package localproxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAcceptsGzip(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"br", false}, {"gzip", true}, {"GZIP; Q=0.5", true},
		{"gzip;q=0", false}, {"*;q=1", true}, {"gzip;q=0, *;q=1", false},
		{"gzip;q=1, gzip;q=0", false}, {"gzip;q=0.001", true}, {"gzip;q=1.000", true},
		{"gzip;q=-1", false}, {"gzip;q=NaN", false}, {"gzip;q=2", false},
		{"gzip;q=0.0001", false}, {"gzip;q=1.001", false}, {"gzip;q=.5", false},
		{"gzip;broken", false}, {"gzip;q=0;q=1", false}, {"gzip;other=yes", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := acceptsGzip([]string{tc.value}); got != tc.want {
				t.Fatalf("acceptsGzip(%q) = %v", tc.value, got)
			}
		})
	}
	if acceptsGzip([]string{"gzip;q=1", "gzip;q=0"}) {
		t.Fatal("multiple header fields bypassed gzip refusal")
	}
}

func gzipRequest(t *testing.T, client *http.Client, url string, header http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestProxyGzipJSONForFixedAndUnknownLength(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		t.Run(fmt.Sprint(fixed), func(t *testing.T) {
			payload := []byte(`{"data":"` + strings.Repeat("compressible-json-", 32*1024) + `"}`)
			url, client := bufferedTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("ETag", `"original"`)
				w.Header().Set("Content-Digest", "original-digest")
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("Vary", "Origin")
				if fixed {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
				}
				for offset := 0; offset < len(payload); offset += 512 {
					_, _ = w.Write(payload[offset:min(offset+512, len(payload))])
					w.(http.Flusher).Flush()
				}
			})
			for _, encoding := range []string{"gzip", "gzip;q=0"} {
				resp := gzipRequest(t, client, url, http.Header{"Accept-Encoding": {encoding}})
				wire, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if len(resp.Cookies()) != 1 || resp.Header.Get("Content-Type") != "application/json" ||
					!strings.Contains(strings.Join(resp.Header.Values("Vary"), ","), "Accept-Encoding") {
					t.Fatal("content type, authentication cookie or Vary changed")
				}
				if encoding != "gzip" {
					if !bytes.Equal(wire, payload) || resp.Header.Get("ETag") != `"original"` || resp.Header.Get("Content-Encoding") != "" {
						t.Fatal("identity variant was transformed")
					}
					continue
				}
				if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("ETag") != "" ||
					resp.Header.Get("Content-Digest") != "" || resp.Header.Get("Accept-Ranges") != "" ||
					resp.ContentLength == int64(len(payload)) {
					t.Fatal("compressed representation retained invalid metadata")
				}
				reader, err := gzip.NewReader(bytes.NewReader(wire))
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(reader)
				reader.Close()
				if err != nil || !bytes.Equal(decoded, payload) || len(wire) >= len(payload)/2 {
					t.Fatalf("gzip response incorrect: %v (%d encoded bytes)", err, len(wire))
				}
			}
		})
	}
}

func TestProxyGzipBypasses(t *testing.T) {
	payload := []byte(strings.Repeat("x", 2*responseBufferSize))
	for _, tc := range []struct {
		name    string
		method  string
		status  int
		header  http.Header
		request http.Header
	}{
		{name: "already-encoded", header: http.Header{"Content-Encoding": {"gzip"}}},
		{name: "response-no-transform", header: http.Header{"Cache-Control": {"private, no-transform"}}},
		{name: "request-no-transform", request: http.Header{"Cache-Control": {"no-transform"}}},
		{name: "range-request", request: http.Header{"Range": {"bytes=0-100"}}},
		{name: "content-range", header: http.Header{"Content-Range": {"bytes 0-100/200"}}},
		{name: "ndjson", header: http.Header{"Content-Type": {"application/x-ndjson"}}},
		{name: "sse", header: http.Header{"Content-Type": {"text/event-stream"}}},
		{name: "explicit-stream", header: http.Header{"X-Accel-Buffering": {"no"}}},
		{name: "declared-trailer", header: http.Header{"Trailer": {"X-Checksum"}}},
		{name: "partial", status: http.StatusPartialContent},
		{name: "head", method: http.MethodHead},
		{name: "not-modified", status: http.StatusNotModified},
		{name: "post", method: http.MethodPost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, client := bufferedTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				for key, values := range tc.header {
					w.Header()[key] = values
				}
				if tc.header.Get("Trailer") == "" {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				if r.Method != http.MethodHead && tc.status != http.StatusNotModified {
					_, _ = w.Write(payload)
				}
			})
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			req, _ := http.NewRequest(method, url, nil)
			req.Header = tc.request.Clone()
			if req.Header == nil {
				req.Header = make(http.Header)
			}
			req.Header.Set("Accept-Encoding", "gzip")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			wire, err := io.ReadAll(resp.Body)
			if err != nil || resp.Header.Get("Content-Encoding") != tc.header.Get("Content-Encoding") {
				t.Fatalf("bypassed response changed: %v", err)
			}
			if method != http.MethodHead && tc.status != http.StatusNotModified && !bytes.Equal(wire, payload) {
				t.Fatal("bypassed response bytes changed")
			}
		})
	}
}

func TestProxyGzipPartialDataAndCancellation(t *testing.T) {
	cancelled := make(chan struct{})
	url, client := bufferedTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"pending":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, len(`{"pending":`))
	if _, err := io.ReadFull(reader, first); err != nil || string(first) != `{"pending":` {
		t.Fatalf("partial compressed JSON was withheld: %v", err)
	}
	cancel()
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("cancelled compression became complete gzip")
	}
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("gzip client cancellation did not reach upstream")
	}
}

func TestProxyGzipTruncation(t *testing.T) {
	url, client := bufferedTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(2*responseBufferSize))
		_, _ = io.WriteString(w, `{"unfinished":`)
		w.(http.Flusher).Flush()
		time.Sleep(2 * responseBufferDelay)
	})
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	reader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("truncated upstream received a complete gzip footer")
	}
}

func TestProxyPreservesUndeclaredTrailers(t *testing.T) {
	for _, encoding := range []string{"identity", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			url, client := bufferedTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
				w.(http.Flusher).Flush()
				w.Header().Set(http.TrailerPrefix+"X-Checksum", "fixture-checksum")
				w.Header().Set(http.TrailerPrefix+"Content-Digest", "original-digest")
			})
			resp := gzipRequest(t, client, url, http.Header{"Accept-Encoding": {encoding}})
			var reader io.Reader = resp.Body
			if encoding == "gzip" {
				compressed, err := gzip.NewReader(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer compressed.Close()
				reader = compressed
			}
			body, err := io.ReadAll(reader)
			if err != nil || string(body) != `{"ok":true}` || resp.Trailer.Get("X-Checksum") != "fixture-checksum" {
				t.Fatalf("late trailer or response lost: %v, trailers %v", err, resp.Trailer)
			}
			if encoding == "gzip" && resp.Trailer.Get("Content-Digest") != "" {
				t.Fatal("original representation digest survived gzip transformation")
			}
		})
	}
}

func TestProxyKnownLengthStreamingBypassesBuffer(t *testing.T) {
	for _, contentType := range []string{"application/x-ndjson", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			t.Cleanup(resume)
			url, client := bufferedTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				w.Header().Set("Content-Length", strconv.Itoa(2*responseBufferSize))
				if contentType == "application/json" {
					w.Header().Set("X-Accel-Buffering", "no")
				}
				_, _ = io.WriteString(w, "first\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			resp := gzipRequest(t, client, url, http.Header{"Accept-Encoding": {"gzip"}})
			if resp.Header.Get("Content-Encoding") != "" {
				t.Fatal("stream bypass was compressed")
			}
			first := make([]byte, 6)
			if _, err := io.ReadFull(resp.Body, first); err != nil || string(first) != "first\n" {
				t.Fatalf("known-length stream delayed first bytes: %v", err)
			}
			resp.Body.Close()
			resume()
		})
	}
}

func TestGzipWriterReuseDoesNotLeakPreviousBody(t *testing.T) {
	for _, payload := range []string{"first-body", "another-body", ""} {
		ctx, cancel := context.WithCancel(context.Background())
		dst := &recordingWriter{header: http.Header{"Content-Type": {"application/json"}}, flushed: make(chan struct{}, 1)}
		w := &responseBuffer{ResponseWriter: dst, method: http.MethodGet, ctx: ctx, cancel: cancel, acceptGzip: true}
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, payload); err != nil {
			t.Fatal(err)
		}
		if err := w.finish(); err != nil {
			t.Fatal(err)
		}
		w.abort()
		cancel()
		wire, _ := dst.snapshot()
		reader, err := gzip.NewReader(bytes.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || string(decoded) != payload {
			t.Fatalf("reused gzip writer leaked a previous response: %v", err)
		}
	}
}
