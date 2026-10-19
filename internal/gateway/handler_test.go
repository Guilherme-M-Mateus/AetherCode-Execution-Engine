package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/model"
	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/queue"
	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/sandbox"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *goredis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func newUnreachablePGPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://test:test@127.0.0.1:1/test")
	if err != nil {
		t.Fatalf("parse pgxpool config: %v", err)
	}
	cfg.MinConns = 0
	cfg.MaxConns = 1
	cfg.ConnConfig.ConnectTimeout = 100 * time.Millisecond
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func fakeJudgeServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"buildVersion":%q}`, version)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleHealth_DegradedWhenDatabaseDown(t *testing.T) {
	judgeSrv := fakeJudgeServer(t, "1.13.0")
	judge := sandbox.NewClient(judgeSrv.URL, "test-token")
	_, rdb := newTestRedis(t)
	pool := newUnreachablePGPool(t)

	submitStream := queue.NewStream(rdb, "ac:submissions:submit", "workers")
	runStream := queue.NewStream(rdb, "ac:submissions:run", "workers")

	h := NewHandler(pool, submitStream, runStream, nil, judge, rdb, nil, nil, 5000, 8)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	h.HandleHealth(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Status != "unhealthy" {
		t.Errorf("status = %q, want %q", resp.Status, "unhealthy")
	}
	if resp.Checks["database"] != "error" {
		t.Errorf("checks.database = %q, want %q", resp.Checks["database"], "error")
	}
	if resp.Checks["sandbox"] != "ok" {
		t.Errorf("checks.sandbox = %q, want %q", resp.Checks["sandbox"], "ok")
	}
	if resp.Checks["redis"] != "ok" {
		t.Errorf("checks.redis = %q, want %q", resp.Checks["redis"], "ok")
	}
	if resp.SandboxVersion != "1.13.0" {
		t.Errorf("sandbox_version = %q, want %q", resp.SandboxVersion, "1.13.0")
	}
}

func TestHandleHealth_ResponseShape(t *testing.T) {
	judgeSrv := fakeJudgeServer(t, "1.13.0")
	judge := sandbox.NewClient(judgeSrv.URL, "test-token")
	_, rdb := newTestRedis(t)
	pool := newUnreachablePGPool(t)

	submitStream := queue.NewStream(rdb, "ac:submissions:submit", "workers")
	runStream := queue.NewStream(rdb, "ac:submissions:run", "workers")

	h := NewHandler(pool, submitStream, runStream, nil, judge, rdb, nil, nil, 5000, 4)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	h.HandleHealth(rec, req)

	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.GoVersion != runtime.Version() {
		t.Errorf("go_version = %q, want %q", resp.GoVersion, runtime.Version())
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds = %d, want >= 0", resp.UptimeSeconds)
	}
	if resp.Workers.Configured != 4 {
		t.Errorf("workers.configured = %d, want 4", resp.Workers.Configured)
	}
	if resp.Workers.Active != 0 {
		t.Errorf("workers.active = %d, want 0", resp.Workers.Active)
	}
	if resp.Checks == nil {
		t.Fatal("checks is nil")
	}
}

func TestHandleHealth_SandboxDown(t *testing.T) {
	judge := sandbox.NewClient("http://127.0.0.1:1", "test-token")
	_, rdb := newTestRedis(t)
	pool := newUnreachablePGPool(t)

	submitStream := queue.NewStream(rdb, "ac:submissions:submit", "workers")
	runStream := queue.NewStream(rdb, "ac:submissions:run", "workers")

	h := NewHandler(pool, submitStream, runStream, nil, judge, rdb, nil, nil, 5000, 8)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	h.HandleHealth(rec, req)

	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Checks["sandbox"] != "error" {
		t.Errorf("checks.sandbox = %q, want %q", resp.Checks["sandbox"], "error")
	}
	if resp.SandboxVersion != "" {
		t.Errorf("sandbox_version = %q, want empty", resp.SandboxVersion)
	}
}

func TestHandleHealth_QueueDepths(t *testing.T) {
	judgeSrv := fakeJudgeServer(t, "1.13.0")
	judge := sandbox.NewClient(judgeSrv.URL, "test-token")
	_, rdb := newTestRedis(t)
	pool := newUnreachablePGPool(t)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		rdb.XAdd(ctx, &goredis.XAddArgs{
			Stream: "ac:submissions:submit",
			Values: map[string]any{"id": i},
		})
	}
	for i := 0; i < 3; i++ {
		rdb.XAdd(ctx, &goredis.XAddArgs{
			Stream: "ac:submissions:run",
			Values: map[string]any{"id": i},
		})
	}

	submitStream := queue.NewStream(rdb, "ac:submissions:submit", "workers")
	runStream := queue.NewStream(rdb, "ac:submissions:run", "workers")

	h := NewHandler(pool, submitStream, runStream, nil, judge, rdb, nil, nil, 5000, 8)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	h.HandleHealth(rec, req)

	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Queues["ac:submissions:submit"] != 5 {
		t.Errorf("submit queue depth = %d, want 5", resp.Queues["ac:submissions:submit"])
	}
	if resp.Queues["ac:submissions:run"] != 3 {
		t.Errorf("run queue depth = %d, want 3", resp.Queues["ac:submissions:run"])
	}
}

func TestHandleLanguages(t *testing.T) {
	judgeSrv := fakeJudgeServer(t, "1.13.0")
	judge := sandbox.NewClient(judgeSrv.URL, "test-token")
	_, rdb := newTestRedis(t)
	pool := newUnreachablePGPool(t)

	submitStream := queue.NewStream(rdb, "ac:submissions:submit", "workers")
	runStream := queue.NewStream(rdb, "ac:submissions:run", "workers")
	h := NewHandler(pool, submitStream, runStream, nil, judge, rdb, nil, nil, 5000, 8)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/languages", nil)
	h.HandleLanguages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var langs []languageInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &langs); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(langs) == 0 {
		t.Fatal("expected at least one language")
	}

	// All sandbox languages must be present
	present := make(map[string]bool, len(langs))
	for _, l := range langs {
		present[l.ID] = true
	}
	for id := range sandbox.Languages {
		if !present[id] {
			t.Errorf("language %q missing from response", id)
		}
	}

	// Verify sorted order
	for i := 1; i < len(langs); i++ {
		if langs[i].ID < langs[i-1].ID {
			t.Errorf("languages not sorted: %q before %q", langs[i-1].ID, langs[i].ID)
		}
	}

	// Spot-check python
	for _, l := range langs {
		if l.ID == "python" {
			if l.Compiled {
				t.Error("python: compiled should be false")
			}
			if l.DefaultCPULimitMs != 2000 {
				t.Errorf("python: default_cpu_limit_ms = %d, want 2000", l.DefaultCPULimitMs)
			}
			if l.DefaultMemoryLimitKb != 524288 {
				t.Errorf("python: default_memory_limit_kb = %d, want 524288", l.DefaultMemoryLimitKb)
			}
		}
		if l.ID == "cpp" {
			if !l.Compiled {
				t.Error("cpp: compiled should be true")
			}
		}
	}
}

func newSubmissionTestHandler(t *testing.T) *Handler {
	t.Helper()
	_, rdb := newTestRedis(t)
	return NewHandler(newUnreachablePGPool(t), nil, nil, nil, nil, rdb, nil, nil, 5000, 8)
}

func TestHandleGetSubmission_InvalidID(t *testing.T) {
	mux := http.NewServeMux()
	newSubmissionTestHandler(t).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/submissions/not-a-uuid", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleGetSubmission_DatabaseDown(t *testing.T) {
	mux := http.NewServeMux()
	newSubmissionTestHandler(t).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/submissions/018f1d2e-3456-7890-abcd-ef0123456789", nil))

	// A DB failure must not be reported as 404 (that would hide outages as "unknown id").
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestBuildSubmissionResponse_PreviewFiltering(t *testing.T) {
	results := []model.TestResult{
		{TestIndex: 0, Verdict: model.VerdictAccepted, StdoutPreview: "sample-out"},
		{TestIndex: 1, Verdict: model.VerdictWrongAnswer, StdoutPreview: "hidden-out"},
	}
	tests := []model.TestCase{{IsSample: true}, {IsSample: false}}

	submit := buildSubmissionResponse(&model.Submission{ID: uuid.New(), Mode: string(model.ModeSubmit)}, results, tests)
	if submit.Results[0].StdoutPreview == nil || *submit.Results[0].StdoutPreview != "sample-out" {
		t.Errorf("submit mode: sample test preview should be kept, got %v", submit.Results[0].StdoutPreview)
	}
	if submit.Results[1].StdoutPreview != nil || submit.Results[1].StderrPreview != nil {
		t.Errorf("submit mode: non-sample test previews should be omitted")
	}

	run := buildSubmissionResponse(&model.Submission{ID: uuid.New(), Mode: string(model.ModeRun)}, results, nil)
	if run.Results[1].StdoutPreview == nil || *run.Results[1].StdoutPreview != "hidden-out" {
		t.Errorf("run mode: all previews should be kept")
	}

	body, _ := json.Marshal(submit)
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	if _, ok := raw["results"].([]any)[1].(map[string]any)["stdout_preview"]; ok {
		t.Errorf("submit mode JSON: stdout_preview key should be absent for non-sample test")
	}
}
