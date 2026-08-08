package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

// Test helper: multipart form content type (set after building form)
var multipartContentType string

// multipartBody creates a multipart form body with a file field.
func multipartBody(t *testing.T, fieldName, filename string, data []byte) *bytes.Buffer {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_ = writer.Close()
	multipartContentType = writer.FormDataContentType()
	return body
}

// fakeZipData returns a minimal valid ZIP file (PK magic bytes).
func fakeZipData() []byte {
	return []byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
}

// fakeGzipData returns a minimal valid GZIP file (magic bytes).
func fakeGzipData() []byte {
	return []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
}

// jsonBody creates a JSON request body.
func jsonBody(v interface{}) *bytes.Buffer {
	data, _ := json.Marshal(v)
	return bytes.NewBuffer(data)
}

// assertJSONSuccess checks that a response has success=true/false.
func assertJSONSuccess(t *testing.T, w *httptest.ResponseRecorder, expectSuccess bool) {
	t.Helper()
	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v (body: %s)", err, w.Body.String())
	}
	if resp.Success != expectSuccess {
		t.Errorf("expected success=%v, got %v (message: %s)", expectSuccess, resp.Success, resp.Message)
	}
}

// testSlogLogger returns a quiet logger for tests.
func testSlogLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
}
