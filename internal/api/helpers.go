package api

import (
"encoding/json"
"net/http"
)

func writeJSON(w http.ResponseWriter, status int, data any) {
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(status)
json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(r *http.Request, dst any) error {
decoder := json.NewDecoder(r.Body)
decoder.DisallowUnknownFields()
return decoder.Decode(dst)
}
