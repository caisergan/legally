package validation

import "context"

// Validator is an independent PAdES validation backend. The signer token worker
// consumes this seam; ExternalValidator and MultiValidator both satisfy it.
type Validator interface {
	Validate(ctx context.Context, signed []byte) (name, version, outcome string, err error)
}

// Result is one independent validator's structured verdict.
type Result struct {
	Name    string
	Version string
	Outcome string // valid | invalid | indeterminate
	Err     error
}

// MultiValidator runs several independent validators and fails closed: the
// aggregate outcome is "valid" only when every validator returns "valid" with no
// error. Any "invalid" wins; otherwise any non-"valid"/error yields
// "indeterminate". Phase 1B requires two independent full PAdES validators.
type MultiValidator struct {
	Validators []Validator
}

// Results runs every validator and returns their individual verdicts.
func (m MultiValidator) Results(ctx context.Context, signed []byte) []Result {
	results := make([]Result, 0, len(m.Validators))
	for _, validator := range m.Validators {
		name, version, outcome, err := validator.Validate(ctx, signed)
		results = append(results, Result{Name: name, Version: version, Outcome: outcome, Err: err})
	}
	return results
}

// Aggregate reduces per-validator verdicts to a single fail-closed outcome.
func Aggregate(results []Result) string {
	if len(results) == 0 {
		return "indeterminate"
	}
	allValid := true
	for _, result := range results {
		if result.Err != nil || result.Outcome == "invalid" {
			return "invalid"
		}
		if result.Outcome != "valid" {
			allValid = false
		}
	}
	if allValid {
		return "valid"
	}
	return "indeterminate"
}

// Validate satisfies Validator so a MultiValidator drops into the single-validator
// seam, reporting a joined name/version and the fail-closed aggregate outcome.
func (m MultiValidator) Validate(ctx context.Context, signed []byte) (string, string, string, error) {
	results := m.Results(ctx, signed)
	name, version := "", ""
	for i, result := range results {
		if i > 0 {
			name += "+"
			version += "+"
		}
		name += result.Name
		version += result.Version
	}
	return name, version, Aggregate(results), nil
}
