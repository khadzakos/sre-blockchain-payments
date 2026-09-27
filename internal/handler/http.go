package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/khadzakos/sre-blockchain-payments/internal/telemetry"

	"github.com/khadzakos/sre-blockchain-payments/internal/domain"
	"github.com/khadzakos/sre-blockchain-payments/internal/service"
	"github.com/khadzakos/sre-blockchain-payments/web"
)

type API struct {
	service *service.Invoices
	log     *zap.Logger
	metrics *telemetry.Metrics
}

func New(s *service.Invoices, log *zap.Logger, metrics *telemetry.Metrics) http.Handler {
	a := &API{service: s, log: log, metrics: metrics}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc(
		"GET /healthz",
		func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) },
	)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.Ready(ctx) != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ready"})
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /api/invoices", a.list)
	api.HandleFunc("POST /api/invoices", a.create)
	api.HandleFunc("GET /api/invoices/{id}", a.get)
	api.HandleFunc("PUT /api/invoices/{id}", a.update)
	api.HandleFunc("DELETE /api/invoices/{id}", a.remove)
	api.HandleFunc("POST /api/invoices/{id}/issue", a.action("issued"))
	api.HandleFunc("POST /api/invoices/{id}/cancel", a.action("cancelled"))
	api.HandleFunc("GET /api/invoices/{id}/events", a.events)
	mux.Handle("/api/", api)
	static := http.FileServer(http.FS(web.Files))
	mux.Handle("GET /{$}", static)
	mux.Handle("GET /app.js", static)
	mux.Handle("GET /style.css", static)
	return a.middleware(mux)
}

type response struct {
	http.ResponseWriter
	status int
}

func (w *response) WriteHeader(status int) { w.status = status; w.ResponseWriter.WriteHeader(status) }

func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var b [12]byte
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().
			Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		parent := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		parent = telemetry.WithRequestID(parent, id)
		ctx, span := otel.Tracer("payments.http").
			Start(parent, "http.request", trace.WithSpanKind(trace.SpanKindServer))
		w.Header().Set("X-Trace-Id", span.SpanContext().TraceID().String())
		rw := &response{w, 200}
		defer func() {
			if recover() != nil {
				span.SetStatus(codes.Error, "panic")
				telemetry.Logger(ctx).Error("request panic")
				write(rw, 500, map[string]string{"error": "Внутренняя ошибка"})
			}
			route := routeName(r.URL.Path)
			method := r.Method
			switch method {
			case "GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS":
			default:
				method = "OTHER"
			}
			span.SetName(method + " " + route)
			span.SetAttributes(
				attribute.String("http.request.method", method),
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", rw.status),
			)
			if rw.status >= 500 {
				span.SetStatus(codes.Error, "HTTP server error")
			}
			a.metrics.Requests.WithLabelValues(method, route, strconv.Itoa(rw.status)).Inc()
			a.metrics.Duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
			telemetry.Logger(ctx).
				Info("http request", zap.String("method", method), zap.String("route", route), zap.Int("status", rw.status), zap.Duration("duration", time.Since(start)))
			span.End()
		}()
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		next.ServeHTTP(rw, r.WithContext(ctx))
	})
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return domain.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return domain.ErrInvalid
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return domain.ErrInvalid
	}
	return nil
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		write(w, 400, map[string]string{"error": err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		write(w, 404, map[string]string{"error": "Счёт не найден"})
	case errors.Is(err, domain.ErrConflict):
		write(w, 409, map[string]string{"error": "Состояние или версия счёта изменились. Обновите данные."})
	default:
		telemetry.Logger(r.Context()).Error("request failed", zap.Error(err))
		write(w, 500, map[string]string{"error": "Не удалось выполнить запрос"})
	}
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func validID(w http.ResponseWriter, r *http.Request) bool {
	if !idPattern.MatchString(r.PathValue("id")) {
		write(w, 404, map[string]string{"error": "Счёт не найден"})
		return false
	}
	return true
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	status := domain.Status(r.URL.Query().Get("status"))
	switch status {
	case "", domain.Draft, domain.Open, domain.Paid, domain.Expired, domain.Cancelled:
	default:
		a.fail(w, r, domain.ErrInvalid)
		return
	}
	limit, offset := 50, 0
	var err error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil {
			a.fail(w, r, domain.ErrInvalid)
			return
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil {
			a.fail(w, r, domain.ErrInvalid)
			return
		}
	}
	if limit < 1 || limit > 100 || offset < 0 {
		a.fail(w, r, domain.ErrInvalid)
		return
	}
	items, err := a.service.List(r.Context(), status, limit, offset)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, items)
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var terms domain.Terms
	if err := decode(w, r, &terms); err != nil {
		a.fail(w, r, err)
		return
	}
	i, err := a.service.Create(r.Context(), terms)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/invoices/"+i.ID)
	write(w, 201, i)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	i, err := a.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, i)
}

func (a *API) update(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	var body struct {
		domain.Terms
		Version int64 `json:"version"`
	}
	if err := decode(w, r, &body); err != nil {
		a.fail(w, r, err)
		return
	}
	i, err := a.service.Change(r.Context(), r.PathValue("id"), body.Version, "updated", body.Terms)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, i)
}

func (a *API) action(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validID(w, r) {
			return
		}
		var body struct {
			Version int64 `json:"version"`
		}
		if err := decode(w, r, &body); err != nil {
			a.fail(w, r, err)
			return
		}
		i, err := a.service.Change(r.Context(), r.PathValue("id"), body.Version, action, domain.Terms{})
		if err != nil {
			a.fail(w, r, err)
			return
		}
		write(w, 200, i)
	}
}

func (a *API) remove(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
	if err != nil {
		a.fail(w, r, domain.ErrInvalid)
		return
	}
	if err = a.service.Delete(r.Context(), r.PathValue("id"), version); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	if !validID(w, r) {
		return
	}
	events, err := a.service.Events(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, events)
}

// Fixed route labels prevent invoice IDs and arbitrary URLs creating metric series.
func routeName(path string) string {
	switch path {
	case "/", "/app.js", "/style.css", "/healthz", "/readyz", "/metrics", "/api/invoices":
		return path
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "invoices" && idPattern.MatchString(parts[2]) {
		if len(parts) == 3 {
			return "/api/invoices/{id}"
		}
		if len(parts) == 4 {
			switch parts[3] {
			case "issue", "cancel", "events":
				return "/api/invoices/{id}/" + parts[3]
			}
		}
	}
	return "unmatched"
}
