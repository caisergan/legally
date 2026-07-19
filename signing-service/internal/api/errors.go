package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
)

func newStrictDecoder(body io.Reader) *json.Decoder {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	return decoder
}

// SafeError is an HTTP-mappable failure that never carries vendor error text.
type SafeError struct {
	Status int
	Code   errcodes.Code
	Detail string
}

func (e SafeError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Detail)
}

func newSafeError(status int, code errcodes.Code, detail string) SafeError {
	return SafeError{Status: status, Code: code, Detail: detail}
}

// writeError renders a SafeError as a JSON ErrorResponse.
func writeError(response http.ResponseWriter, err SafeError) {
	writeJSON(response, err.Status, ErrorResponse{Code: string(err.Code), Detail: err.Detail})
}

// decodeStrict decodes a JSON body rejecting unknown fields and trailing data.
func decodeStrict(body io.Reader, target any) error {
	decoder := newStrictDecoder(body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("unexpected trailing content after JSON body")
	}
	return nil
}
