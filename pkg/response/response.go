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

// PaginatedResponse represents a paginated JSON response with metadata at root level.
type PaginatedResponse struct {
	Success  bool        `json:"success"`
	Message  string      `json:"message,omitempty"`
	Data     interface{} `json:"data"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
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

// Paginated sends a 200 OK response with pagination metadata at root level.
func Paginated(w http.ResponseWriter, message string, data interface{}, total, page, pageSize int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	resp := PaginatedResponse{
		Success:  true,
		Message:  message,
		Data:     data,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// Error sends an error response with custom status code and messages.
func Error(w http.ResponseWriter, status int, message string, errs interface{}) {
	JSON(w, status, false, message, nil, errs)
}
