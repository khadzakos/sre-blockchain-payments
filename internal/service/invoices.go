package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/khadzakos/sre-blockchain-payments/internal/domain"
	"github.com/khadzakos/sre-blockchain-payments/internal/telemetry"
)

type Repository interface {
	Create(context.Context, domain.Invoice, domain.Event) error
	Get(context.Context, string) (domain.Invoice, error)
	List(context.Context, domain.Status, int, int) ([]domain.Invoice, error)
	Save(context.Context, domain.Invoice, int64, domain.Event) error
	Delete(context.Context, string, int64) error
	Events(context.Context, string) ([]domain.Event, error)
	Due(context.Context, time.Time, int) ([]domain.Invoice, error)
	Ping(context.Context) error
}

type Invoices struct {
	repo Repository
	now  func() time.Time
}

func New(repo Repository) *Invoices {
	return &Invoices{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Invoices) Ready(ctx context.Context) (err error) {
	ctx, finish := telemetry.Start(ctx, "service.Ready")
	defer func() { finish(err) }()
	return s.repo.Ping(ctx)
}

func (s *Invoices) Get(ctx context.Context, id string) (out domain.Invoice, err error) {
	ctx, finish := telemetry.Start(ctx, "service.Get")
	defer func() { finish(err) }()
	return s.repo.Get(ctx, id)
}

func (s *Invoices) List(
	ctx context.Context,
	status domain.Status,
	limit, offset int,
) (out []domain.Invoice, err error) {
	ctx, finish := telemetry.Start(ctx, "service.List")
	defer func() { finish(err) }()
	return s.repo.List(ctx, status, limit, offset)
}

func (s *Invoices) Events(ctx context.Context, id string) (out []domain.Event, err error) {
	ctx, finish := telemetry.Start(ctx, "service.Events")
	defer func() { finish(err) }()
	if _, err := s.repo.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.repo.Events(ctx, id)
}

func (s *Invoices) Create(ctx context.Context, terms domain.Terms) (out domain.Invoice, err error) {
	ctx, finish := telemetry.Start(ctx, "service.Create")
	defer func() { finish(err) }()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return domain.Invoice{}, err
	}
	i, err := domain.NewInvoice(hex.EncodeToString(b[:]), terms, s.now())
	if err != nil {
		return i, err
	}
	err = s.repo.Create(ctx, i, event(i, "created"))
	return i, err
}

func event(i domain.Invoice, kind string) domain.Event {
	return domain.Event{InvoiceID: i.ID, Type: kind, OccurredAt: i.UpdatedAt, Version: i.Version}
}

func (s *Invoices) Change(
	ctx context.Context,
	id string,
	version int64,
	action string,
	terms domain.Terms,
) (out domain.Invoice, err error) {
	ctx, finish := telemetry.Start(ctx, "service.Change")
	defer func() { finish(err) }()
	i, err := s.repo.Get(ctx, id)
	if err != nil {
		return i, err
	}
	if version < 1 || version != i.Version {
		return i, domain.ErrConflict
	}
	now := s.now()
	switch action {
	case "updated":
		err = i.Edit(terms, now)
	case "issued":
		err = i.Issue(now)
	case "cancelled":
		err = i.Cancel(now)
	case "expired":
		err = i.Expire(now)
	default:
		err = domain.ErrInvalid
	}
	if err != nil {
		return i, err
	}
	i.Version++
	err = s.repo.Save(ctx, i, version, event(i, action))
	return i, err
}

func (s *Invoices) Delete(ctx context.Context, id string, version int64) (err error) {
	ctx, finish := telemetry.Start(ctx, "service.Delete")
	defer func() { finish(err) }()
	if version < 1 {
		return domain.ErrInvalid
	}
	return s.repo.Delete(ctx, id, version)
}

func (s *Invoices) ExpireDue(ctx context.Context) (out int, err error) {
	ctx, finish := telemetry.Start(ctx, "service.ExpireDue")
	defer func() { finish(err) }()
	invoices, err := s.repo.Due(ctx, s.now(), 100)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, i := range invoices {
		_, err = s.Change(ctx, i.ID, i.Version, "expired", domain.Terms{})
		if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
