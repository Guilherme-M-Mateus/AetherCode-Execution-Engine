package worker

import (
	"strings"
	"testing"

	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/model"
	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/sandbox"
)

// TestJudgeOutput pins down the output-comparison contract: trailing
// whitespace/newlines are ignored, but any real difference is a wrong answer.
func TestJudgeOutput(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		actual   string
		want     model.Verdict
	}{
		{"exact match", "42\n", "42\n", model.VerdictAccepted},
		{"trailing newline stripped", "42", "42\n", model.VerdictAccepted},
		{"trailing spaces", "42   ", "42", model.VerdictAccepted},
		{"multiple trailing newlines", "42\n\n\n", "42", model.VerdictAccepted},
		{"line-level trailing ws", "a  \nb\t\n", "a\nb\n", model.VerdictAccepted},
		{"crlf line endings", "42\r\n", "42\n", model.VerdictAccepted},
		{"empty vs empty", "", "", model.VerdictAccepted},
		{"different values", "42", "43", model.VerdictWrongAnswer},
		{"empty vs content", "", "x", model.VerdictWrongAnswer},
		{"internal newlines preserved", "a\nb\nc", "a\nb\nd", model.VerdictWrongAnswer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JudgeOutput(c.expected, c.actual, CheckerExact); got != c.want {
				t.Fatalf("JudgeOutput(%q, %q) = %q, want %q", c.expected, c.actual, got, c.want)
			}
		})
	}
}

// TestNormalizeOutput checks the per-line trimming helper directly.
func TestNormalizeOutput(t *testing.T) {
	cases := map[string]string{
		"a  \n b  \n": "a\n b",
		"line\n\n\n":  "line",
		"pad\t\t":     "pad",
		"a\r\nb\r\n":  "a\nb",
		"no-change":   "no-change",
		"":            "",
	}
	for in, want := range cases {
		if got := normalizeOutput(in); got != want {
			t.Errorf("normalizeOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJudgeOutputNormalizesCarriageReturns(t *testing.T) {
	if JudgeOutput("1\n2\n", "1\r\n2\r\n", CheckerExact) != model.VerdictAccepted {
		t.Fatal("CRLF output should match LF expected")
	}
	if JudgeOutput("1\n2", "1\r2", CheckerExact) != model.VerdictAccepted {
		t.Fatal("lone CR should count as a line break")
	}
}

func TestJudgeOutputTokenChecker(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		actual   string
		want     model.Verdict
	}{
		{"exact match", "1 2 3\n", "1 2 3\n", model.VerdictAccepted},
		{"whitespace layout ignored", "1 2 3", "1\n2\t\t3  \n\n", model.VerdictAccepted},
		{"empty vs empty", "", "  \n", model.VerdictAccepted},
		{"different token", "1 2 3", "1 2 4", model.VerdictWrongAnswer},
		{"token order matters", "1 2 3", "3 2 1", model.VerdictWrongAnswer},
		{"extra tokens", "1 2", "1 2 3", model.VerdictWrongAnswer},
		{"fewer tokens", "1 2 3", "1 2", model.VerdictWrongAnswer},
		{"no float tolerance", "1.0", "1.0000001", model.VerdictWrongAnswer},
		{"case sensitive", "YES", "yes", model.VerdictWrongAnswer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JudgeOutput(c.expected, c.actual, CheckerToken); got != c.want {
				t.Fatalf("JudgeOutput(%q, %q, token) = %q, want %q", c.expected, c.actual, got, c.want)
			}
		})
	}
}

func TestJudgeOutputFloatChecker(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		actual   string
		want     model.Verdict
	}{
		{"exact match", "3.141592653589\n", "3.141592653589\n", model.VerdictAccepted},
		{"different formatting, same value", "0.5", "5e-1", model.VerdictAccepted},
		{"off by less than epsilon", "3.141592653589", "3.1415931", model.VerdictAccepted},
		{"off by exactly epsilon", "1.0", "1.000001", model.VerdictAccepted},
		{"off by more than epsilon", "3.141592653589", "3.14159", model.VerdictWrongAnswer},
		{"negative over epsilon", "-2.5", "-2.500002", model.VerdictWrongAnswer},
		{"list within epsilon", "1.0 2.0 3.0", "1.0000001\n2\n2.9999999", model.VerdictAccepted},
		{"one list value over epsilon", "1.0 2.0 3.0", "1.0 2.1 3.0", model.VerdictWrongAnswer},
		{"extra tokens", "1.0 2.0", "1.0 2.0 3.0", model.VerdictWrongAnswer},
		{"fewer tokens", "1.0 2.0 3.0", "1.0 2.0", model.VerdictWrongAnswer},
		{"non-numeric output", "1.0", "one", model.VerdictWrongAnswer},
		{"nan never matches", "1.0", "NaN", model.VerdictWrongAnswer},
		{"inf matches +Inf", "inf", "+Inf", model.VerdictAccepted},
		{"inf vs -inf", "inf", "-inf", model.VerdictWrongAnswer},
		{"inf vs large finite", "inf", "1e308", model.VerdictWrongAnswer},
		{"mixed words and numbers", "Case 1: 0.333333", "Case 1: 0.3333333", model.VerdictAccepted},
		{"mixed words must match", "Case 1: 0.5", "case 1: 0.5", model.VerdictWrongAnswer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JudgeOutput(c.expected, c.actual, CheckerFloat); got != c.want {
				t.Fatalf("JudgeOutput(%q, %q, float) = %q, want %q", c.expected, c.actual, got, c.want)
			}
		})
	}
}

func TestJudgeOutputUnknownCheckerFallsBackToExact(t *testing.T) {
	if JudgeOutput("1 2", "1  2", "levenshtein") != model.VerdictWrongAnswer {
		t.Fatal("unknown checker should compare exactly, not token-wise")
	}
	if JudgeOutput("42\n", "42", "") != model.VerdictAccepted {
		t.Fatal("empty checker should behave like exact")
	}
	for _, c := range []string{CheckerExact, CheckerToken, CheckerFloat} {
		if !knownChecker(c) {
			t.Errorf("knownChecker(%q) = false", c)
		}
	}
	if knownChecker("levenshtein") {
		t.Error(`knownChecker("levenshtein") = true`)
	}
}

func TestJudgeResultUsesChecker(t *testing.T) {
	r := sandbox.Result{Status: sandbox.StatusAccepted, Files: map[string]string{"stdout": "1.0000001\n"}}
	if v := judgeResult(r, 0, "1.0", CheckerFloat).Verdict; v != model.VerdictAccepted {
		t.Fatalf("float checker: got %q, want accepted", v)
	}
	if v := judgeResult(r, 0, "1.0", CheckerExact).Verdict; v != model.VerdictWrongAnswer {
		t.Fatalf("exact checker: got %q, want wrong_answer", v)
	}
}

func BenchmarkNormalizeOutput(b *testing.B) {
	// Typical output: 50 lines with trailing spaces and newlines
	var sb strings.Builder
	for range 50 {
		sb.WriteString("Hello World 42  \n")
	}
	s := sb.String()
	b.ResetTimer()
	for range b.N {
		normalizeOutput(s)
	}
}
