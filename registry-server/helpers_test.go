package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
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

// fakeZipData returns a structurally valid provider ZIP.
func fakeZipData() []byte {
	var body bytes.Buffer
	zw := zip.NewWriter(&body)
	entry, _ := zw.Create("terraform-provider-aws_v1.0.0")
	_, _ = entry.Write([]byte("provider binary"))
	_ = zw.Close()
	return body.Bytes()
}

// fakeGzipData returns a structurally valid Terraform module tarball.
func fakeGzipData() []byte {
	var body bytes.Buffer
	gw := gzip.NewWriter(&body)
	tw := tar.NewWriter(gw)
	content := []byte("terraform {}\n")
	_ = tw.WriteHeader(&tar.Header{Name: "main.tf", Mode: 0644, Size: int64(len(content))})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gw.Close()
	return body.Bytes()
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
