package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/database"
	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	server  *http.Server
	pool    *pgxpool.Pool
	session *discordgo.Session
}

func New(addr string, pool *pgxpool.Pool, session *discordgo.Session) *Server {
	mux := http.NewServeMux()
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	instance := &Server{server: server, pool: pool, session: session}
	mux.HandleFunc("/healthz", instance.health)
	mux.HandleFunc("/readyz", instance.ready)
	return instance
}

func (s *Server) ListenAndServe() error { return s.server.ListenAndServe() }

func (s *Server) Shutdown(ctx context.Context) error { return s.server.Shutdown(ctx) }

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	if err := database.Health(context.Background(), s.pool); err != nil {
		writeHealth(w, http.StatusServiceUnavailable, map[string]string{"postgres": "down"})
		return
	}
	writeHealth(w, http.StatusOK, map[string]string{"postgres": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	if s.session == nil || s.session.DataReady == false {
		writeHealth(w, http.StatusServiceUnavailable, map[string]string{"discord": "not_ready"})
		return
	}
	if err := database.Health(context.Background(), s.pool); err != nil {
		writeHealth(w, http.StatusServiceUnavailable, map[string]string{"postgres": "down"})
		return
	}
	writeHealth(w, http.StatusOK, map[string]string{"discord": "ready", "postgres": "ok"})
}

func writeHealth(w http.ResponseWriter, status int, payload map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
