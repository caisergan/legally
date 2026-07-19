package validation

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubValidator struct {
	name, version, outcome string
	err                    error
}

func (s stubValidator) Validate(context.Context, []byte) (string, string, string, error) {
	return s.name, s.version, s.outcome, s.err
}

func TestNormalizeOutcomeFailsClosedOnNegativeMarker(t *testing.T) {
	// "Signature is Invalid" must never normalize to "valid" (contains "valid").
	path, digest := writeExecutable(t, "echo Signature is Invalid")
	validator := ExternalValidator{Name: "poppler", Version: "1", Path: path, SHA256: digest, Timeout: 5 * time.Second, MaxOutputLen: 4096}
	if _, _, outcome, err := validator.Validate(context.Background(), []byte("pdf")); err != nil || outcome != "invalid" {
		t.Fatalf("outcome=%q err=%v, want invalid", outcome, err)
	}
}

func TestMultiValidatorAggregate(t *testing.T) {
	cases := []struct {
		name    string
		results []Result
		want    string
	}{
		{"both valid", []Result{{Outcome: "valid"}, {Outcome: "valid"}}, "valid"},
		{"one invalid wins", []Result{{Outcome: "valid"}, {Outcome: "invalid"}}, "invalid"},
		{"error is invalid", []Result{{Outcome: "valid"}, {Err: errors.New("x")}}, "invalid"},
		{"indeterminate blocks valid", []Result{{Outcome: "valid"}, {Outcome: "indeterminate"}}, "indeterminate"},
		{"empty", nil, "indeterminate"},
	}
	for _, tc := range cases {
		if got := Aggregate(tc.results); got != tc.want {
			t.Errorf("%s: Aggregate = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMultiValidatorValidateFailsClosed(t *testing.T) {
	m := MultiValidator{Validators: []Validator{
		stubValidator{name: "pdfsig", version: "26", outcome: "valid"},
		stubValidator{name: "pyhanko", version: "0.35", outcome: "invalid"},
	}}
	name, version, outcome, err := m.Validate(context.Background(), []byte("pdf"))
	if outcome != "invalid" || err != nil {
		t.Fatalf("outcome=%q err=%v, want invalid", outcome, err)
	}
	if name != "pdfsig+pyhanko" || version != "26+0.35" {
		t.Fatalf("joined name/version = %q %q", name, version)
	}
}

func TestReleaseFailsClosed(t *testing.T) {
	cases := []struct {
		outcome TrustOutcome
		release bool
	}{
		{TrustOutcome{"valid", "valid", "valid"}, true},
		{TrustOutcome{"valid", "indeterminate", "indeterminate"}, false}, // self-signed corpus
		{TrustOutcome{"valid", "valid", "indeterminate"}, false},
		{TrustOutcome{"invalid", "valid", "valid"}, false},
		{TrustOutcome{"valid", "invalid", "valid"}, false},
	}
	for _, tc := range cases {
		if got := tc.outcome.ReleaseAllowed(); got != tc.release {
			t.Errorf("%+v: ReleaseAllowed = %v, want %v", tc.outcome, got, tc.release)
		}
	}
}

func TestMultiValidatorTwoIndependentValid(t *testing.T) {
	m := MultiValidator{Validators: []Validator{
		stubValidator{name: "pdfsig", version: "26", outcome: "valid"},
		stubValidator{name: "pyhanko", version: "0.35", outcome: "valid"},
	}}
	if _, _, outcome, _ := m.Validate(context.Background(), []byte("pdf")); outcome != "valid" {
		t.Fatalf("outcome=%q, want valid", outcome)
	}
}
