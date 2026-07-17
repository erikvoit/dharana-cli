package output

import (
	"encoding/json"
	"fmt"
	"io"
)

type Envelope struct {
	OK    bool        `json:"ok"`
	Data  any         `json:"data,omitempty"`
	Error *ErrorValue `json:"error,omitempty"`
}

type ErrorValue struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Candidates any    `json:"candidates,omitempty"`
	Details    any    `json:"details,omitempty"`
}

type AppError struct {
	Code       string
	Message    string
	Candidates any
	Details    any
}

func (e *AppError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

func NewErrorWithCandidates(code, message string, candidates any) *AppError {
	return &AppError{Code: code, Message: message, Candidates: candidates}
}

func NewErrorWithDetails(code, message string, details any) *AppError {
	return &AppError{Code: code, Message: message, Details: details}
}

func WriteJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Envelope{OK: true, Data: data})
}

func WriteErrorJSON(w io.Writer, err error) error {
	appErr, ok := err.(*AppError)
	if !ok {
		appErr = NewError("INTERNAL_ERROR", "An unexpected error occurred.")
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Envelope{
		OK: false,
		Error: &ErrorValue{
			Code:       appErr.Code,
			Message:    appErr.Message,
			Candidates: appErr.Candidates,
			Details:    appErr.Details,
		},
	})
}
