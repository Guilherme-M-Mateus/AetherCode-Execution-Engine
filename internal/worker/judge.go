package worker

import (
	"math"
	"strconv"
	"strings"

	"github.com/stjosephsplacements/AetherCode-Execution-Engine/internal/model"
)

// Checker types stored in problem_versions.checker_type.
const (
	CheckerExact = "exact" // per-line trailing whitespace ignored, otherwise byte-for-byte
	CheckerToken = "token" // whitespace-separated tokens must match one-for-one
	CheckerFloat = "float" // like token, but numeric tokens match within floatEpsilon
)

// floatEpsilon is the absolute tolerance used by the float checker.
const floatEpsilon = 1e-6

// knownChecker reports whether checkerType is one JudgeOutput understands.
func knownChecker(checkerType string) bool {
	switch checkerType {
	case CheckerExact, CheckerToken, CheckerFloat:
		return true
	}
	return false
}

// JudgeOutput compares expected and actual output using checkerType.
// Unknown checker types fall back to exact; callers should validate first
// (see loadTests) so the fallback is logged once per job, not once per test.
func JudgeOutput(expected, actual, checkerType string) model.Verdict {
	var ok bool
	switch checkerType {
	case CheckerToken:
		ok = tokensMatch(expected, actual, false)
	case CheckerFloat:
		ok = tokensMatch(expected, actual, true)
	default:
		// Exact: both are trimmed of trailing whitespace per line and trailing newlines.
		ok = normalizeOutput(expected) == normalizeOutput(actual)
	}
	if ok {
		return model.VerdictAccepted
	}
	return model.VerdictWrongAnswer
}

// tokensMatch splits both outputs on any whitespace and compares them token by
// token. With floats set, an expected token that parses as a number matches an
// actual number within floatEpsilon; non-numeric tokens must match exactly.
func tokensMatch(expected, actual string, floats bool) bool {
	exp, act := strings.Fields(expected), strings.Fields(actual)
	if len(exp) != len(act) {
		return false
	}
	for i := range exp {
		if exp[i] == act[i] {
			continue
		}
		if !floats {
			return false
		}
		e, errE := strconv.ParseFloat(exp[i], 64)
		a, errA := strconv.ParseFloat(act[i], 64)
		// e == a covers equal infinities (Inf-Inf is NaN); NaN never satisfies
		// <=, so "nan" output can't pass a numeric answer.
		if errE != nil || errA != nil || (e != a && !(math.Abs(e-a) <= floatEpsilon)) {
			return false
		}
	}
	return true
}

func normalizeOutput(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lineStart := 0
	trailingWS := -1 // index of first trailing whitespace char in current line
	pendingNL := 0   // empty lines buffered; flushed only when non-empty content follows
	wroteAny := false

	flush := func(line string) {
		if wroteAny {
			for range pendingNL {
				b.WriteByte('\n')
			}
		}
		pendingNL = 0
		if wroteAny {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		wroteAny = true
	}

	emitLine := func(end int) {
		if trailingWS >= 0 && trailingWS < end {
			end = trailingWS
		}
		line := s[lineStart:end]
		if len(line) == 0 {
			if wroteAny {
				pendingNL++
			}
		} else {
			flush(line)
		}
		trailingWS = -1
	}

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r':
			emitLine(i)
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			lineStart = i + 1
		case '\n':
			emitLine(i)
			lineStart = i + 1
		case ' ', '\t':
			if trailingWS < 0 {
				trailingWS = i
			}
		default:
			trailingWS = -1
		}
	}

	// Final line (no trailing newline in input)
	end := len(s)
	if trailingWS >= 0 && trailingWS < end {
		end = trailingWS
	}
	if last := s[lineStart:end]; len(last) > 0 {
		flush(last)
	}
	return b.String()
}
