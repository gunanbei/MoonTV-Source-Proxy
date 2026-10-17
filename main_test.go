package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProxyPreservesRequestAndResponseMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api" || r.URL.RawQuery != "a=1" {
			t.Errorf("request was not forwarded transparently: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Test") != "kept" || r.Header.Get("Cookie") != "session=in" || r.Header.Get("User-Agent") != "client-test" || r.Header.Get("Range") != "bytes=0-10" || !strings.HasPrefix(r.Header.Get("X-Forwarded-For"), "client-proxy") {
			t.Errorf("request headers were not preserved: %+v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "request-body" {
			t.Errorf("request body was not preserved: %q", body)
		}
		w.Header().Set("X-Upstream", "kept")
		w.Header().Set("Set-Cookie", "session=test")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("plain upstream response"))
	}))
	defer upstream.Close()

	proxyURL := "http://proxy.local/api?proxy=" + url.QueryEscape(upstream.URL) + "&a=1"
	request := httptest.NewRequest(http.MethodPost, proxyURL, strings.NewReader("request-body"))
	request.Header.Set("Authorization", "Bearer test")
	request.Header.Set("X-Test", "kept")
	request.Header.Set("Cookie", "session=in")
	request.Header.Set("User-Agent", "client-test")
	request.Header.Set("Range", "bytes=0-10")
	request.Header.Set("X-Forwarded-For", "client-proxy")
	response := httptest.NewRecorder()

	newHandler(http.DefaultTransport).ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("X-Upstream") != "kept" || response.Header().Get("Set-Cookie") != "session=test" {
		t.Fatalf("upstream metadata was not preserved: status=%d headers=%v", response.Code, response.Header())
	}
	if response.Body.String() != "plain upstream response" {
		t.Fatalf("upstream body was not preserved: %q", response.Body.String())
	}
}

func TestParseProxyTarget(t *testing.T) {
	requestURL, err := url.Parse("http://proxy.local/?proxy=https://api.example.com/api/provide/vod/&ac=videolist&ids=638645694114038172")
	if err != nil {
		t.Fatal(err)
	}
	target, query, err := parseProxyTarget(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	if target.String() != "https://api.example.com/api/provide/vod/" {
		t.Fatalf("unexpected target: %s", target)
	}
	if query != "ac=videolist&ids=638645694114038172" && query != "ids=638645694114038172&ac=videolist" {
		t.Fatalf("unexpected forwarded query: %s", query)
	}
}

func TestParseProxyTargetSupportsAppendedAPIQuery(t *testing.T) {
	requestURL, err := url.Parse("http://proxy.local/?proxy=https%3A%2F%2Fapi.example.com%2Fapi%2Fprovide%2Fvod%2F?ac=videolist&ids=638645694114038172")
	if err != nil {
		t.Fatal(err)
	}
	target, query, err := parseProxyTarget(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	if target.String() != "https://api.example.com/api/provide/vod/?ac=videolist" {
		t.Fatalf("unexpected target: %s", target)
	}
	if query != "ac=videolist&ids=638645694114038172" && query != "ids=638645694114038172&ac=videolist" {
		t.Fatalf("unexpected forwarded query: %s", query)
	}
}

func TestModifyJSONResponseConvertsRecursively(t *testing.T) {
	body := `{"list":[{"vod_id":638645694114038172,"nested":[{"id":9007199254740992}],"title":"id 638645694114038172"}]}`
	response := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
	if err := modifyJSONResponse(response); err != nil {
		t.Fatalf("response modifier returned error: %v", err)
	}
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"list":[{"vod_id":"638645694114038172","nested":[{"id":"9007199254740992"}],"title":"id 638645694114038172"}]}`
	if string(got) != want {
		t.Fatalf("unexpected response:\n got: %s\nwant: %s", got, want)
	}
}

func TestModifyJSONResponseFailsOpenOnInvalidJSON(t *testing.T) {
	body := `{"vod_id":638645694114038172, broken`
	response := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
	if err := modifyJSONResponse(response); err != nil {
		t.Fatalf("invalid upstream JSON must not fail the proxy: %v", err)
	}
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("invalid response was not preserved: %q", got)
	}
}

func TestModifyJSONResponseFailsOpenOnLargeBody(t *testing.T) {
	body := append([]byte(`{"data":"`), bytes.Repeat([]byte("x"), maxJSONBody)...)
	body = append(body, []byte(`"}`)...)
	response := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
	if err := modifyJSONResponse(response); err != nil {
		t.Fatalf("large upstream response must not fail the proxy: %v", err)
	}
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("large response was not preserved: got %d bytes, want %d", len(got), len(body))
	}
}

func TestModifyJSONResponseSkipsCompressedBody(t *testing.T) {
	body := `{"vod_id":638645694114038172}`
	response := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}, "Content-Encoding": []string{"gzip"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
	if err := modifyJSONResponse(response); err != nil {
		t.Fatalf("compressed upstream response must not fail the proxy: %v", err)
	}
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("compressed response was unexpectedly modified: %s", got)
	}
}

func TestStringifyUnsafeIntegers(t *testing.T) {
	input := []byte(`{"list":[{"vod_id":638645694114038172,"id":9007199254740992,"type_id":12,"vod_total":57,"note":638645694114038172,"nested":{"item_ids":[1,9007199254740993]}}]}`)
	got, changed := stringifyUnsafeIntegers(input)
	if !changed {
		t.Fatal("expected response to be changed")
	}
	want := `{"list":[{"vod_id":"638645694114038172","id":"9007199254740992","type_id":12,"vod_total":57,"note":"638645694114038172","nested":{"item_ids":[1,"9007199254740993"]}}]}`
	if string(got) != want {
		t.Fatalf("unexpected normalized JSON:\n got: %s\nwant: %s", got, want)
	}
}

func TestStringifyUnsafeIntegersLeavesStringsAndSafeNumbers(t *testing.T) {
	input := []byte(`{"vod_id":"638645694114038172","id":9007199254740991,"type_id":-9007199254740991}`)
	got, changed := stringifyUnsafeIntegers(input)
	if changed {
		t.Fatal("safe numbers or existing strings should not be changed")
	}
	if !bytes.Equal(got, input) {
		t.Fatalf("response bytes changed:\n got: %s\nwant: %s", got, input)
	}
}
