package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestModifyJSONResponseConvertsRecursively(t *testing.T) {
	body := `{"list":[{"vod_id":638645694114038172,"nested":[{"id":9007199254740992}],"title":"id 638645694114038172"}]}`
	response := &http.Response{
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
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
	response := &http.Response{
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
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
	response := &http.Response{
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
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
	response := &http.Response{
		Header:        http.Header{"Content-Type": []string{"application/json"}, "Content-Encoding": []string{"gzip"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
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
