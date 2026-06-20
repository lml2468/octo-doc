package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mininglamp-OSS/octo-doc/internal/platform/apperr"
)

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr maps an error to its HTTP response. apperr.Error carries status+code;
// anything else becomes a 500 with a generic message (details logged).
func writeErr(w http.ResponseWriter, logger *slog.Logger, err error) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		if ae.Status == 429 {
			w.Header().Set("Retry-After", itoa(ae.RetryAfter))
			writeJSON(w, 429, map[string]any{"error": ae.Code, "message": ae.Msg, "retry_after": ae.RetryAfter})
			return
		}
		if ae.Status >= 500 {
			logger.Error("request failed", "code", ae.Code, "err", ae.Msg)
		} else {
			logger.Info("request error", "code", ae.Code, "msg", ae.Msg)
		}
		writeJSON(w, ae.Status, map[string]any{"error": ae.Code, "message": ae.Msg})
		return
	}
	logger.Error("unhandled error", "err", err.Error())
	writeJSON(w, 500, map[string]any{"error": "internal_error", "message": "an unexpected error occurred"})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
