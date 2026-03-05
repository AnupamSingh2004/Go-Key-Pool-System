package api

import (
"net/http"
)

// NewRouter creates the HTTP mux with all routes registered.
func NewRouter(srv *Server) http.Handler {
mux := http.NewServeMux()

// Public routes
mux.HandleFunc("/health", srv.HealthCheck)
mux.HandleFunc("/api/requests/", srv.routeRequests)
mux.HandleFunc("/api/requests", srv.SubmitRequest)

// Admin routes (auth required)
admin := AdminAuth(srv.Cfg.AdminToken, srv.Logger)
mux.Handle("/admin/keys/", admin(http.HandlerFunc(srv.routeAdminKeys)))
mux.Handle("/admin/keys", admin(http.HandlerFunc(srv.routeAdminKeysList)))
mux.Handle("/admin/health", admin(http.HandlerFunc(srv.PoolHealth)))
mux.Handle("/admin/config", admin(http.HandlerFunc(srv.routeAdminConfig)))

// Wrap with request logger
return RequestLogger(srv.Logger)(mux)
}

// routeRequests dispatches /api/requests/{id} based on method.
func (s *Server) routeRequests(w http.ResponseWriter, r *http.Request) {
s.GetRequestStatus(w, r)
}

// routeAdminKeysList dispatches /admin/keys (no trailing slash).
func (s *Server) routeAdminKeysList(w http.ResponseWriter, r *http.Request) {
switch r.Method {
case http.MethodGet:
s.ListKeys(w, r)
case http.MethodPost:
s.AddKey(w, r)
default:
writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
}

// routeAdminKeys dispatches /admin/keys/{id}... based on path and method.
func (s *Server) routeAdminKeys(w http.ResponseWriter, r *http.Request) {
path := r.URL.Path

switch {
case pathEndsWith(path, "/reset"):
s.ResetKeyCircuit(w, r)
case pathEndsWith(path, "/weight"):
s.UpdateKeyWeight(w, r)
default:
s.DeleteKey(w, r)
}
}

// routeAdminConfig dispatches /admin/config based on method.
func (s *Server) routeAdminConfig(w http.ResponseWriter, r *http.Request) {
switch r.Method {
case http.MethodGet:
s.GetConfig(w, r)
case http.MethodPut:
s.UpdateConfig(w, r)
default:
writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
}

func pathEndsWith(path, suffix string) bool {
return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix
}
