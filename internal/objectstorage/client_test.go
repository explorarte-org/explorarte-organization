package objectstorage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	keyPath, _ := writeTestPrivateKey(t)
	cfg := Config{
		Enabled:          true,
		TenancyOCID:      "ocid1.tenancy.oc1..aaaa",
		UserOCID:         "ocid1.user.oc1..aaaa",
		Fingerprint:      "0b:a6:4b:35:a0:b2:19:73:ae:04:75:ca:17:dc:6c:ad",
		Region:           "sa-santiago-1",
		Namespace:        "axkhdnwe6r1c",
		Bucket:           "explorarte-org-knowledge-source",
		PrivateKeyFile:   keyPath,
		RequestTimeout:   defaultRequestTimeout,
		MaxResponseBytes: defaultMaxResponseBytes,
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.httpClient = server.Client()
	client.overrideBaseURLForTest(server.URL)
	return client, server
}

func TestListObjectsFollowsPagination(t *testing.T) {
	calls := 0
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "" {
			t.Errorf("missing Authorization header")
		}
		if r.URL.Query().Get("prefix") != "raw/" {
			t.Errorf("prefix = %q, want raw/", r.URL.Query().Get("prefix"))
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("start") == "" {
			_ = json.NewEncoder(w).Encode(listObjectsResponse{
				Objects:       []ObjectSummary{{Name: "raw/a.pdf", Size: 10}},
				NextStartWith: "raw/b.pdf",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(listObjectsResponse{
			Objects: []ObjectSummary{{Name: "raw/b.pdf", Size: 20}},
		})
	})

	objects, err := client.ListObjects(context.Background(), "raw/")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("got %d objects, want 2", len(objects))
	}
	if calls != 2 {
		t.Fatalf("got %d calls, want 2 (pagination)", calls)
	}
}

func TestGetObjectReturnsBody(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/o/raw/doc.pdf") {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte("pdf-bytes"))
	})

	body, err := client.GetObject(context.Background(), "raw/doc.pdf")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(body) != "pdf-bytes" {
		t.Fatalf("body = %q", body)
	}
}

func TestGetObjectMapsNotFound(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := client.GetObject(context.Background(), "raw/missing.pdf")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !isNotFound(err) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPutObjectSendsBodyAndSignedHeaders(t *testing.T) {
	var receivedContentType string
	var receivedSHA256 string
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %q, want PUT", r.Method)
		}
		receivedContentType = r.Header.Get("Content-Type")
		receivedSHA256 = r.Header.Get("X-Content-Sha256")
		w.WriteHeader(http.StatusOK)
	})

	err := client.PutObject(context.Background(), "manifests/foo.json", []byte(`{"a":1}`), "application/json")
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if receivedContentType != "application/json" {
		t.Fatalf("content-type = %q", receivedContentType)
	}
	if receivedSHA256 == "" {
		t.Fatalf("expected x-content-sha256 header to be set")
	}
}

func TestDoRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 64))
	}))
	t.Cleanup(server.Close)

	keyPath, _ := writeTestPrivateKey(t)
	cfg := Config{
		Enabled: true, TenancyOCID: "ocid1.tenancy.oc1..aaaa", UserOCID: "ocid1.user.oc1..aaaa",
		Fingerprint: "0b:a6:4b:35:a0:b2:19:73:ae:04:75:ca:17:dc:6c:ad", Region: "sa-santiago-1",
		Namespace: "axkhdnwe6r1c", Bucket: "explorarte-org-knowledge-source",
		PrivateKeyFile: keyPath, RequestTimeout: defaultRequestTimeout, MaxResponseBytes: 32,
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.httpClient = server.Client()
	client.overrideBaseURLForTest(server.URL)

	if _, err := client.GetObject(context.Background(), "raw/big.bin"); err == nil {
		t.Fatalf("expected error for oversized response")
	}
}

func isNotFound(err error) bool {
	for err != nil {
		if err == ErrNotFound {
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}
