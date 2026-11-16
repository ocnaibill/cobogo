package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// maxBodyBytes é o limite geral do corpo de uma requisição (DEC-27 item 14).
const maxBodyBytes = 1 << 20

// errorBody é o formato único de erro da API (DEC-27 item 15): um código
// estável para a interface e uma mensagem em português.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "unavailable"
	}
	return "internal"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeCode(w, status, codeFor(status), message)
}

func writeCode(w http.ResponseWriter, status int, code, message string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, message
	writeJSON(w, status, b)
}

// readJSON lê exatamente um documento JSON do corpo, sem campos
// desconhecidos, até maxBodyBytes. Em caso de erro, já responde e devolve
// false. A mensagem de erro nunca ecoa o corpo (pode conter senha).
func readJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	if limit <= 0 {
		limit = maxBodyBytes
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, http.StatusRequestEntityTooLarge, "o corpo da requisição excede o limite")
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "o corpo da requisição está vazio")
		default:
			writeError(w, http.StatusBadRequest, "corpo inválido: envie um único objeto JSON com os campos documentados")
		}
		return false
	}
	// Só um documento: qualquer coisa depois do objeto é recusada.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "corpo inválido: envie um único objeto JSON")
		return false
	}
	// Precisa ser um objeto (nem `null`, nem lista), sem campos desconhecidos.
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		writeError(w, http.StatusBadRequest, "corpo inválido: envie um objeto JSON")
		return false
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "corpo inválido: envie um único objeto JSON com os campos documentados")
		return false
	}
	return true
}

func isJSON(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/json")
}
