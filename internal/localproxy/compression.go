package localproxy

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

var gzipWriters = sync.Pool{New: func() any {
	writer, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	if err != nil {
		panic(err) // BestSpeed is a fixed supported constant.
	}
	return writer
}}

// An explicit gzip refusal overrides wildcard acceptance, including duplicates.
func acceptsGzip(values []string) bool {
	specific, wildcard := -1, -1
	for _, entry := range strings.Split(strings.Join(values, ","), ",") {
		coding, params, err := mime.ParseMediaType(strings.TrimSpace(entry))
		name := strings.TrimSpace(strings.SplitN(entry, ";", 2)[0])
		if !strings.EqualFold(name, "gzip") && name != "*" {
			continue
		}
		quality := 1000
		if err != nil {
			quality = 0
		} else {
			for key, value := range params {
				if key != "q" {
					quality = 0
					break
				}
				quality = encodingQuality(value)
			}
		}
		if strings.EqualFold(coding, "gzip") || strings.EqualFold(name, "gzip") {
			if specific < 0 || quality < specific {
				specific = quality
			}
		} else if wildcard < 0 || quality < wildcard {
			wildcard = quality
		}
	}
	if specific >= 0 {
		return specific > 0
	}
	return wildcard > 0
}

func encodingQuality(value string) int {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || (parts[0] != "0" && parts[0] != "1") {
		return 0
	}
	if len(parts) == 1 {
		if parts[0] == "1" {
			return 1000
		}
		return 0
	}
	fraction := parts[1]
	if len(fraction) > 3 {
		return 0
	}
	for _, digit := range fraction {
		if digit < '0' || digit > '9' || (parts[0] == "1" && digit != '0') {
			return 0
		}
	}
	if parts[0] == "1" {
		return 1000
	}
	n, _ := strconv.Atoi(fraction + strings.Repeat("0", 3-len(fraction)))
	return n
}

func hasNoTransform(header http.Header) bool {
	for _, value := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(strings.SplitN(directive, "=", 2)[0]), "no-transform") {
				return true
			}
		}
	}
	return false
}

func varyAcceptEncoding(header http.Header) {
	for _, value := range header.Values("Vary") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name == "*" || strings.EqualFold(name, "Accept-Encoding") {
				return
			}
		}
	}
	header.Add("Vary", "Accept-Encoding")
}

func stripRepresentationValidators(header http.Header, prefix string) {
	for key := range header {
		for _, validator := range []string{"ETag", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
			if strings.EqualFold(key, prefix+validator) {
				delete(header, key)
				break
			}
		}
	}
}
