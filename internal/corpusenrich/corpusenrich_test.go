package corpusenrich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func fakeServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	return srv, srv.Close
}

func TestFetchBatchParsesRealShapedResponse(t *testing.T) {
	srv, closeFn := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]map[string]any{
			{"paperId": "id1", "title": "Paper One", "abstract": "Abstract one.", "year": 2024, "externalIds": map[string]string{"ArXiv": "2401.00001"}},
			nil,
		})
	})
	defer closeFn()
	client := &Client{HTTPClient: srv.Client()}
	orig := batchEndpointForTest(srv.URL)
	defer orig()

	records, err := client.FetchBatch(context.Background(), []string{"id1", "id-missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].PaperID != "id1" || !records[0].HasAbstract() {
		t.Fatalf("record0=%+v", records[0])
	}
	if records[1].PaperID != "id-missing" || records[1].HasAbstract() {
		t.Fatalf("record1=%+v, expected empty abstract for a null entry", records[1])
	}
}

func TestFetchBatchReturnsErrRateLimitedOn429(t *testing.T) {
	srv, closeFn := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer closeFn()
	client := &Client{HTTPClient: srv.Client()}
	orig := batchEndpointForTest(srv.URL)
	defer orig()

	_, err := client.FetchBatch(context.Background(), []string{"id1"})
	if err != ErrRateLimited {
		t.Fatalf("err=%v, expected ErrRateLimited", err)
	}
}

func TestFetchBatchRejectsOversizedBatch(t *testing.T) {
	client := &Client{HTTPClient: http.DefaultClient}
	ids := make([]string, 501)
	_, err := client.FetchBatch(context.Background(), ids)
	if err == nil {
		t.Fatal("expected an error for a batch over Semantic Scholar's 500-ID cap")
	}
}

func TestStoreResumeSkipsAlreadyFetched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abstracts.jsonl")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Put(AbstractRecord{PaperID: "id1", Title: "T", Abstract: "A"})
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Has("id1") {
		t.Fatal("expected id1 to resume as already-fetched")
	}
	if reopened.Has("id-never-seen") {
		t.Fatal("id-never-seen should not be present")
	}
}

func TestOrchestratorRunSkipsCachedAndReportsCoverage(t *testing.T) {
	srv, closeFn := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IDs []string `json:"ids"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		out := make([]map[string]any, len(req.IDs))
		for i, id := range req.IDs {
			out[i] = map[string]any{"paperId": id, "title": "T", "abstract": "A"}
		}
		json.NewEncoder(w).Encode(out)
	})
	defer closeFn()
	orig := batchEndpointForTest(srv.URL)
	defer orig()

	path := filepath.Join(t.TempDir(), "abstracts.jsonl")
	store, _ := OpenStore(path)
	store.Put(AbstractRecord{PaperID: "already-cached", Abstract: "A"})
	store.Flush()

	reopened, _ := OpenStore(path)
	orchestrator := &Orchestrator{
		Client: &Client{HTTPClient: srv.Client()}, Store: reopened,
		Config: OrchestratorConfig{BatchSize: 2, MaxRetriesOn429: 1, BackoffBase: 0, InterBatchDelay: 0, FlushEveryBatch: true},
	}
	result, err := orchestrator.Run(context.Background(), []string{"already-cached", "new1", "new2", "new3"})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyCached != 1 || result.Fetched != 3 || result.AbstractsFound != 3 {
		t.Fatalf("result=%+v", result)
	}
}

func TestOrchestratorStopsOnPersistentRateLimit(t *testing.T) {
	srv, closeFn := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer closeFn()
	orig := batchEndpointForTest(srv.URL)
	defer orig()

	path := filepath.Join(t.TempDir(), "abstracts.jsonl")
	store, _ := OpenStore(path)
	orchestrator := &Orchestrator{
		Client: &Client{HTTPClient: srv.Client()}, Store: store,
		Config: OrchestratorConfig{BatchSize: 2, MaxRetriesOn429: 2, BackoffBase: 0, InterBatchDelay: 0, FlushEveryBatch: true},
	}
	result, err := orchestrator.Run(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RateLimitedStop {
		t.Fatalf("expected RateLimitedStop=true, got %+v", result)
	}
}
