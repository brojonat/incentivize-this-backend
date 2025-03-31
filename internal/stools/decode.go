package stools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxBytesError represents an error when the request body exceeds the maximum allowed size
type MaxBytesError struct {
	Message string
}

func (e *MaxBytesError) Error() string {
	return e.Message
}

// MalformedJSONError represents an error when the request body contains malformed JSON
type MalformedJSONError struct {
	Message string
}

func (e *MalformedJSONError) Error() string {
	return e.Message
}

// DecodeJSONBody decodes a JSON request body into the provided struct.
// It handles various common errors that can occur when dealing with JSON request bodies.
func DecodeJSONBody(r *http.Request, dst interface{}) error {
	// If Content-Type is not application/json, return an error
	contentType := r.Header.Get("Content-Type")
	if contentType != "" {
		if !strings.Contains(contentType, "application/json") {
			return &MalformedJSONError{
				Message: "Content-Type header is not application/json",
			}
		}
	}

	// Use http.MaxBytesReader to limit the size of the request body
	r.Body = http.MaxBytesReader(nil, r.Body, 1048576) // 1MB limit

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError
		var invalidUnmarshalError *json.InvalidUnmarshalError

		switch {
		case errors.As(err, &syntaxError):
			return &MalformedJSONError{
				Message: fmt.Sprintf("Request body contains malformed JSON (at position %d)", syntaxError.Offset),
			}

		case errors.Is(err, io.ErrUnexpectedEOF):
			return &MalformedJSONError{
				Message: "Request body contains malformed JSON",
			}

		case errors.As(err, &unmarshalTypeError):
			return &MalformedJSONError{
				Message: fmt.Sprintf("Request body contains an invalid value for the %q field (at position %d)", unmarshalTypeError.Field, unmarshalTypeError.Offset),
			}

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return &MalformedJSONError{
				Message: fmt.Sprintf("Request body contains unknown field %s", fieldName),
			}

		case errors.Is(err, io.EOF):
			return &MalformedJSONError{
				Message: "Request body must not be empty",
			}

		case err.Error() == "http: request body too large":
			return &MaxBytesError{
				Message: "Request body must not be larger than 1MB",
			}

		case errors.As(err, &invalidUnmarshalError):
			// This is likely a developer error, like passing a non-pointer to Decode
			return fmt.Errorf("invalid unmarshal error: %w", err)

		default:
			return fmt.Errorf("error decoding JSON: %w", err)
		}
	}

	// Check if there are any additional fields in the JSON that were not used
	err = dec.Decode(&struct{}{})
	if err != io.EOF {
		return &MalformedJSONError{
			Message: "Request body must only contain a single JSON object",
		}
	}

	return nil
}
