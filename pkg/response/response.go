package response

import (
	"encoding/json"
	"net/http"
)

// Response represents a standard JSON response structure for the API.
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Errors  interface{} `json:"errors,omitempty"`
}

// JSON sends a structured JSON response to the client.
func JSON(w http.ResponseWriter, status int, success bool, message string, data interface{}, errs interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := Response{
		Success: success,
		Message: message,
		Data:    data,
		Errors:  errs,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// OK sends a 200 OK standard success response.
func OK(w http.ResponseWriter, message string, data interface{}) {
	JSON(w, http.StatusOK, true, message, data, nil)
}

// Error sends an error response with custom status code and messages.
func Error(w http.ResponseWriter, status int, message string, errs interface{}) {
	JSON(w, status, false, message, nil, errs)
}
