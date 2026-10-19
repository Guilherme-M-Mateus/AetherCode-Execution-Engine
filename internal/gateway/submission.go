package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/auth"
	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/db"
	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/model"
)

type submissionResponse struct {
	JobID         string               `json:"job_id"`
	Status        model.Status         `json:"status"`
	Verdict       model.Verdict        `json:"verdict,omitempty"`
	Language      string               `json:"language"`
	Mode          string               `json:"mode"`
	CPUTimeNs     int64                `json:"cpu_time_ns"`
	MemoryBytes   int64                `json:"memory_bytes"`
	TestCount     int                  `json:"test_count"`
	TestsPassed   int                  `json:"tests_passed"`
	CompileStderr string               `json:"compile_stderr,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
	Results       []submissionTestView `json:"results"`
}

type submissionTestView struct {
	TestIndex     int           `json:"test_index"`
	Verdict       model.Verdict `json:"verdict"`
	CPUTimeNs     int64         `json:"cpu_time_ns"`
	MemoryBytes   int64         `json:"memory_bytes"`
	StdoutPreview *string       `json:"stdout_preview,omitempty"`
	StderrPreview *string       `json:"stderr_preview,omitempty"`
}

// HandleGetSubmission returns a submission's status, verdict and per-test results,
// so clients that missed the SSE stream can poll for the outcome.
func (h *Handler) HandleGetSubmission(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		jsonError(w, "invalid submission id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	sub, err := db.GetSubmission(ctx, h.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("get submission failed", "submission_id", id, "err", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Same ownership rules as the SSE stream: authenticated callers must own the
	// submission; unauthenticated callers need the stream_token of a run-mode job.
	identity, hasAuth := auth.GetIdentity(ctx)
	if hasAuth {
		if sub.TenantID != identity.TenantID || sub.UserID == nil || *sub.UserID != identity.UserID {
			jsonError(w, "not found", http.StatusNotFound)
			return
		}
	} else {
		if sub.ProblemVersionID != nil {
			jsonError(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if !verifyStreamToken(ctx, h.rdb, id.String(), r.URL.Query().Get("stream_token")) {
			jsonError(w, "invalid or missing stream_token", http.StatusUnauthorized)
			return
		}
	}

	results, err := db.GetTestResults(ctx, h.pool, id)
	if err != nil {
		slog.Error("get test results failed", "submission_id", id, "err", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	var tests []model.TestCase
	if sub.Mode == string(model.ModeSubmit) && sub.ProblemVersionID != nil {
		tests, err = db.GetTestCases(ctx, h.pool, *sub.ProblemVersionID)
		if err != nil {
			slog.Error("get test cases failed", "submission_id", id, "err", err)
			jsonError(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(buildSubmissionResponse(sub, results, tests)) //nolint:errcheck // best-effort: write the submission
}

// buildSubmissionResponse shapes a submission for the API. In submit mode, output
// previews are kept only for sample tests (same rule as the worker's VERDICT event,
// where test_index is the position in the ordinal-ordered test case list).
func buildSubmissionResponse(sub *model.Submission, results []model.TestResult, tests []model.TestCase) submissionResponse {
	isSubmit := sub.Mode == string(model.ModeSubmit)
	views := make([]submissionTestView, len(results))
	for i, res := range results {
		v := submissionTestView{
			TestIndex:   res.TestIndex,
			Verdict:     res.Verdict,
			CPUTimeNs:   res.CPUTimeNs,
			MemoryBytes: res.MemoryBytes,
		}
		if !isSubmit || (res.TestIndex >= 0 && res.TestIndex < len(tests) && tests[res.TestIndex].IsSample) {
			v.StdoutPreview = &res.StdoutPreview
			v.StderrPreview = &res.StderrPreview
		}
		views[i] = v
	}
	return submissionResponse{
		JobID:         sub.ID.String(),
		Status:        sub.Status,
		Verdict:       sub.Verdict,
		Language:      sub.Language,
		Mode:          sub.Mode,
		CPUTimeNs:     sub.CPUTimeNs,
		MemoryBytes:   sub.MemoryBytes,
		TestCount:     sub.TestCount,
		TestsPassed:   sub.TestsPassed,
		CompileStderr: sub.CompileStderr,
		CreatedAt:     sub.CreatedAt,
		Results:       views,
	}
}
