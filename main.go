package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxJSONBody = 32 << 20

func main() {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
	}

	mux := newHandler(transport)

	listen := os.Getenv("LISTEN_ADDR")
	if listen == "" {
		listen = ":8080"
	}
	server := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("video source proxy listening on %s", listen)
	log.Fatal(server.ListenAndServe())
}

func newHandler(transport http.RoundTripper) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		target, query, err := parseProxyTarget(r.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = transport
		originalDirector := proxy.Director
		proxy.Director = func(request *http.Request) {
			originalDirector(request)
			request.URL.RawQuery = query
		}
		proxy.ModifyResponse = modifyJSONResponse
		proxy.ServeHTTP(w, r)
	})
	return mux
}

// parseProxyTarget reads the upstream from ?proxy=<url>. Query parameters
// already present in the upstream URL and other request parameters are forwarded.
func parseProxyTarget(requestURL *url.URL) (*url.URL, string, error) {
	values, err := url.ParseQuery(requestURL.RawQuery)
	if err != nil {
		return nil, "", fmt.Errorf("invalid proxy query")
	}
	upstream := strings.TrimSpace(values.Get("proxy"))
	if upstream == "" {
		return nil, "", fmt.Errorf("missing proxy query parameter")
	}
	target, err := url.Parse(upstream)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, "", fmt.Errorf("proxy must be an absolute http(s) URL")
	}

	forwardedQuery := target.RawQuery
	for key, items := range values {
		if key == "proxy" {
			continue
		}
		for _, item := range items {
			forwardedQuery = appendQuery(forwardedQuery, url.QueryEscape(key)+"="+url.QueryEscape(item))
		}
	}
	return target, forwardedQuery, nil
}

func appendQuery(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}
	return current + "&" + next
}

func modifyJSONResponse(response *http.Response) error {
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if (!strings.Contains(contentType, "application/json") && !strings.Contains(contentType, "+json")) || response.Header.Get("Content-Encoding") != "" {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxJSONBody+1))
	if err != nil || len(body) > maxJSONBody {
		// Fail open: restore consumed bytes and let the original upstream body continue.
		response.Body = &readCloser{
			Reader: io.MultiReader(bytes.NewReader(body), response.Body),
			Closer: response.Body,
		}
		return nil
	}
	response.Body.Close()

	// Invalid or unsupported JSON is returned byte-for-byte as received.
	if !json.Valid(body) {
		response.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}

	fixed, changed := stringifyUnsafeIntegers(body)
	if !changed || !json.Valid(fixed) {
		response.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}

	response.Body = io.NopCloser(bytes.NewReader(fixed))
	response.ContentLength = int64(len(fixed))
	response.Header.Set("Content-Length", strconv.Itoa(len(fixed)))
	response.Header.Del("Content-Encoding")
	response.Header.Del("ETag")
	response.Header.Del("Content-MD5")
	return nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

// stringifyUnsafeIntegers converts unsafe JSON integer tokens to strings and
// leaves quoted strings, decimals, and safe integers unchanged.
func stringifyUnsafeIntegers(src []byte) ([]byte, bool) {
	var out bytes.Buffer
	out.Grow(len(src) + 32)
	changed := false

	for i := 0; i < len(src); {
		if src[i] == '"' {
			end := quotedStringEnd(src, i)
			if end < 0 {
				out.Write(src[i:])
				break
			}
			out.Write(src[i : end+1])
			i = end + 1
			continue
		}
		if src[i] == '-' || (src[i] >= '0' && src[i] <= '9') {
			end := integerEnd(src, i)
			if end > 0 && unsafeInteger(src[i:end]) {
				out.WriteByte('"')
				out.Write(src[i:end])
				out.WriteByte('"')
				i = end
				changed = true
				continue
			}
		}
		out.WriteByte(src[i])
		i++
	}
	if !changed {
		return src, false
	}
	return out.Bytes(), true
}

func quotedStringEnd(src []byte, start int) int {
	escaped := false
	for i := start + 1; i < len(src); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch src[i] {
		case '\\':
			escaped = true
		case '"':
			return i
		}
	}
	return -1
}

func integerEnd(src []byte, start int) int {
	i := start
	if i < len(src) && src[i] == '-' {
		i++
	}
	digits := i
	for i < len(src) && src[i] >= '0' && src[i] <= '9' {
		i++
	}
	if i == digits || (i < len(src) && (src[i] == '.' || src[i] == 'e' || src[i] == 'E')) {
		return -1
	}
	return i
}

func unsafeInteger(number []byte) bool {
	value := strings.TrimLeft(strings.TrimPrefix(string(number), "-"), "0")
	const limit = "9007199254740991"
	return value != "" && (len(value) > len(limit) || (len(value) == len(limit) && value > limit))
}
