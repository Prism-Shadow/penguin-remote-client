package localproxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestGzipPoolUsesBestSpeed(t *testing.T) {
	payload := []byte(strings.Repeat("alpha beta gamma delta alphabet alphabet ", 10000))
	var expected bytes.Buffer
	fresh, err := gzip.NewWriterLevel(&expected, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 5; iteration++ {
		var got bytes.Buffer
		pooled := gzipWriters.Get().(*gzip.Writer)
		pooled.Reset(&got)
		if _, err := pooled.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := pooled.Close(); err != nil {
			t.Fatal(err)
		}
		pooled.Reset(io.Discard)
		gzipWriters.Put(pooled)
		if !bytes.Equal(got.Bytes(), expected.Bytes()) {
			t.Fatal("gzip pool did not use BestSpeed")
		}
	}
}

func TestProxyIgnoresFormerGzipLevelOverride(t *testing.T) {
	for _, value := range []string{"3", "6", "invalid", ""} {
		t.Run("former-level-"+value, func(t *testing.T) {
			t.Setenv("PENGUIN_REMOTE_HTTP_GZIP_LEVEL", value)
			payload := []byte(`{"data":"` + strings.Repeat("fixture-", responseBufferSize) + `"}`)
			url, client := bufferedTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
				_, _ = w.Write(payload)
			})
			resp := gzipRequest(t, client, url, http.Header{"Accept-Encoding": {"gzip"}})
			if resp.Header.Get("Content-Encoding") != "gzip" {
				t.Fatal("former override prevented gzip")
			}
			reader, err := gzip.NewReader(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			decoded, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(decoded, payload) {
				t.Fatalf("former override changed response: %v", err)
			}
		})
	}
}

func TestGzipConcurrentIntegrityAndAbort(t *testing.T) {
	// Equal-length overlapping bodies expose stale writer state and cross-request data.
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 4; iteration++ {
				payload := []byte(`{"data":"` + strings.Repeat(strconv.Itoa(worker)+"/"+strconv.Itoa(iteration)+" lorem ipsum ", 20000) + `"}`)
				ctx, cancel := context.WithCancel(context.Background())
				dst := &recordingWriter{header: http.Header{"Content-Type": {"application/json"}}, flushed: make(chan struct{}, 1)}
				w := &responseBuffer{ResponseWriter: dst, method: http.MethodGet, ctx: ctx, cancel: cancel, acceptGzip: true}
				if iteration == 0 {
					if _, err := w.Write(payload[:1024]); err != nil {
						t.Error(err)
					}
					cancel()
					if err := w.finish(); err == nil {
						t.Error("cancelled buffer finished")
					}
					w.abort()
					if w.gzip != nil || w.timer != nil || !w.closed {
						t.Error("abort retained resources")
					}
					continue
				}
				if _, err := w.Write(payload); err != nil {
					t.Error(err)
				}
				if err := w.finish(); err != nil {
					t.Error(err)
				}
				w.abort()
				cancel()
				wire, _ := dst.snapshot()
				reader, err := gzip.NewReader(bytes.NewReader(wire))
				if err != nil {
					t.Error(err)
					continue
				}
				decoded, err := io.ReadAll(reader)
				reader.Close()
				if err != nil || !bytes.Equal(decoded, payload) {
					t.Errorf("concurrent body mismatch: %v", err)
				}
			}
		}(worker)
	}
	wg.Wait()
}
