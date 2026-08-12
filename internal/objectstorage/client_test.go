package objectstorage

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestDeleteObjectSucceeds(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := client.DeleteObject(context.Background(), "raw/gone.pdf"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
}

func TestDeleteObjectTreatsNotFoundAsSuccess(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if err := client.DeleteObject(context.Background(), "raw/already-gone.pdf"); err != nil {
		t.Fatalf("DeleteObject on missing key should not error, got: %v", err)
	}
}

// --- HeadObject -------------------------------------------------------

func TestHeadObjectReadsSizeFromContentLength(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %q, want HEAD", r.Method)
		}
		w.Header().Set("Content-Length", "1234")
		w.Header().Set("Content-MD5", "abc123==")
		w.WriteHeader(http.StatusOK)
	})

	summary, err := client.HeadObject(context.Background(), "raw/doc.pdf")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if summary.Size != 1234 {
		t.Fatalf("Size = %d, want 1234", summary.Size)
	}
	if summary.MD5 != "abc123==" {
		t.Fatalf("MD5 = %q, want abc123==", summary.MD5)
	}
}

func TestHeadObjectToleratesMissingContentLength(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	summary, err := client.HeadObject(context.Background(), "raw/doc.pdf")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if summary.Size != 0 {
		t.Fatalf("Size = %d, want 0 when Content-Length absent", summary.Size)
	}
}

// --- PutObjectIfAbsent --------------------------------------------------

func md5B64(b []byte) string {
	sum := md5.Sum(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func TestPutObjectIfAbsentCreatesWhenMissing(t *testing.T) {
	var gotIfNoneMatch string
	var gotContentLength string
	body := []byte(`{"page":1}`)

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %q, want PUT", r.Method)
		}
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		gotContentLength = r.Header.Get("Content-Length")
		w.WriteHeader(http.StatusOK)
	})

	result, err := client.PutObjectIfAbsent(context.Background(), "manifests/parser-runs/x/poppler/1.0/manifest.json", body, "application/json")
	if err != nil {
		t.Fatalf("PutObjectIfAbsent: %v", err)
	}
	if result.Outcome != PutOutcomeCreated {
		t.Fatalf("Outcome = %v, want PutOutcomeCreated", result.Outcome)
	}
	if gotIfNoneMatch != "*" {
		t.Fatalf("If-None-Match = %q, want \"*\"", gotIfNoneMatch)
	}
	if gotContentLength != strconv.Itoa(len(body)) {
		t.Fatalf("Content-Length = %q, want %d", gotContentLength, len(body))
	}
	if result.Object.MD5 != md5B64(body) {
		t.Fatalf("Object.MD5 = %q, want %q", result.Object.MD5, md5B64(body))
	}
	if result.Object.Size != int64(len(body)) {
		t.Fatalf("Object.Size = %d, want %d", result.Object.Size, len(body))
	}
}

func TestPutObjectIfAbsentReusesWhenDigestMatches(t *testing.T) {
	body := []byte(`{"page":1}`)
	putCalls, headCalls := 0, 0

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putCalls++
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"code":"ObjectAlreadyExists","message":"object already exists"}`, http.StatusPreconditionFailed)
		case http.MethodHead:
			headCalls++
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Header().Set("Content-MD5", md5B64(body))
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})

	result, err := client.PutObjectIfAbsent(context.Background(), "manifests/parser-runs/x/poppler/1.0/manifest.json", body, "application/json")
	if err != nil {
		t.Fatalf("PutObjectIfAbsent: %v", err)
	}
	if result.Outcome != PutOutcomeReused {
		t.Fatalf("Outcome = %v, want PutOutcomeReused", result.Outcome)
	}
	if putCalls != 1 || headCalls != 1 {
		t.Fatalf("putCalls=%d headCalls=%d, want 1 and 1", putCalls, headCalls)
	}
}

func TestPutObjectIfAbsentConflictsWhenDigestDiffers(t *testing.T) {
	body := []byte(`{"page":1}`)
	existingBody := []byte(`{"page":1,"different":true}`)

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			http.Error(w, `{"code":"ObjectAlreadyExists","message":"object already exists"}`, http.StatusPreconditionFailed)
		case http.MethodHead:
			w.Header().Set("Content-Length", strconv.Itoa(len(existingBody)))
			w.Header().Set("Content-MD5", md5B64(existingBody))
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})

	_, err := client.PutObjectIfAbsent(context.Background(), "manifests/parser-runs/x/poppler/1.0/manifest.json", body, "application/json")
	if !errors.Is(err, ErrImmutableObjectConflict) {
		t.Fatalf("err = %v, want ErrImmutableObjectConflict", err)
	}
}

func TestPutObjectIfAbsentFallsBackToBodyWhenHeadHasNoMD5(t *testing.T) {
	body := []byte(`page-bytes-identical`)

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			http.Error(w, `{"code":"ObjectAlreadyExists","message":"object already exists"}`, http.StatusPreconditionFailed)
		case http.MethodHead:
			// Multipart-uploaded objects: OCI omits Content-MD5.
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})

	result, err := client.PutObjectIfAbsent(context.Background(), "pages/x/poppler/1.0/page-0001-y.pdf", body, "application/pdf")
	if err != nil {
		t.Fatalf("PutObjectIfAbsent: %v", err)
	}
	if result.Outcome != PutOutcomeReused {
		t.Fatalf("Outcome = %v, want PutOutcomeReused", result.Outcome)
	}
}

func TestPutObjectIfAbsentFallbackConflictsOnDifferentBytes(t *testing.T) {
	body := []byte(`page-bytes-a`)
	existingBody := []byte(`page-bytes-b-different-length`)

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			http.Error(w, `{"code":"ObjectAlreadyExists","message":"object already exists"}`, http.StatusPreconditionFailed)
		case http.MethodHead:
			w.Header().Set("Content-Length", strconv.Itoa(len(existingBody)))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			_, _ = w.Write(existingBody)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})

	_, err := client.PutObjectIfAbsent(context.Background(), "pages/x/poppler/1.0/page-0001-y.pdf", body, "application/pdf")
	if !errors.Is(err, ErrImmutableObjectConflict) {
		t.Fatalf("err = %v, want ErrImmutableObjectConflict", err)
	}
}

func TestPutObjectIfAbsentPropagatesOtherErrors(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := client.PutObjectIfAbsent(context.Background(), "manifests/parser-runs/x/poppler/1.0/manifest.json", []byte("x"), "application/json")
	if err == nil {
		t.Fatalf("expected error")
	}
	if errors.Is(err, ErrImmutableObjectConflict) {
		t.Fatalf("500 must not be reported as ErrImmutableObjectConflict")
	}
	if !errors.Is(err, ErrRequest) {
		t.Fatalf("err = %v, want wrapping ErrRequest", err)
	}
}

// --- error sanitization -------------------------------------------------

func TestErrorSanitizationNeverEmbedsRawBody(t *testing.T) {
	secret := "super-secret-internal-detail-should-never-appear-in-logs-0xdeadbeef"
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(secret))
	})

	_, err := client.GetObject(context.Background(), "raw/x.pdf")
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error must not contain the raw response body verbatim: %v", err)
	}
}

func TestErrorSanitizationSurfacesCodeAndMessageOnly(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"InternalError","message":"boom"}`, http.StatusInternalServerError)
	})

	_, err := client.GetObject(context.Background(), "raw/x.pdf")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "code=InternalError") || !strings.Contains(err.Error(), "message=boom") {
		t.Fatalf("expected sanitized code/message in error, got: %v", err)
	}
}

func TestErrorSanitizationTruncatesLongFields(t *testing.T) {
	longMessage := strings.Repeat("x", 5000)
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]string{"code": "InternalError", "message": longMessage})
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(body)
	})

	_, err := client.GetObject(context.Background(), "raw/x.pdf")
	if err == nil {
		t.Fatalf("expected error")
	}
	if len(err.Error()) > 500 {
		t.Fatalf("error message not bounded: %d bytes", len(err.Error()))
	}
}
