package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/khadzakos/sre-blockchain-payments/internal/domain"
	"github.com/khadzakos/sre-blockchain-payments/migrations"
)

type Postgres struct{ pool *pgxpool.Pool }

func New(ctx context.Context, url string) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("invalid database configuration")
	}
	config.MaxConns = 10
	config.ConnConfig.Tracer = queryTracer{}
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	p, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("database unavailable")
	}
	return &Postgres{p}, nil
}
func (p *Postgres) Close()                         { p.pool.Close() }
func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

const columns = `id,description,amount::text,chain_id,token_address,recipient_address,expires_at,status,version,created_at,updated_at,issued_at,cancelled_at,paid_at`

func scan(row pgx.Row) (domain.Invoice, error) {
	var i domain.Invoice
	err := row.Scan(
		&i.ID,
		&i.Description,
		&i.Amount,
		&i.ChainID,
		&i.TokenAddress,
		&i.RecipientAddress,
		&i.ExpiresAt,
		&i.Status,
		&i.Version,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.IssuedAt,
		&i.CancelledAt,
		&i.PaidAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return i, err
}

func (p *Postgres) Get(ctx context.Context, id string) (domain.Invoice, error) {
	return scan(p.pool.QueryRow(ctx, "SELECT "+columns+" FROM invoices WHERE id=$1", id))
}

func collect(rows pgx.Rows) ([]domain.Invoice, error) {
	defer rows.Close()
	out := []domain.Invoice{}
	for rows.Next() {
		i, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (p *Postgres) List(
	ctx context.Context,
	status domain.Status,
	limit, offset int,
) ([]domain.Invoice, error) {
	rows, err := p.pool.Query(
		ctx,
		"SELECT "+columns+" FROM invoices WHERE ($1='' OR status=$1) ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3",
		string(status),
		limit,
		offset,
	)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

func (p *Postgres) Due(ctx context.Context, now time.Time, limit int) ([]domain.Invoice, error) {
	rows, err := p.pool.Query(
		ctx,
		"SELECT "+columns+" FROM invoices WHERE status='open' AND expires_at<=$1 ORDER BY expires_at,id LIMIT $2",
		now,
		limit,
	)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

func appendEvent(ctx context.Context, tx pgx.Tx, e domain.Event) error {
	_, err := tx.Exec(
		ctx,
		"INSERT INTO invoice_events(invoice_id,type,occurred_at,version) VALUES($1,$2,$3,$4)",
		e.InvoiceID,
		e.Type,
		e.OccurredAt,
		e.Version,
	)
	return err
}

func (p *Postgres) Create(ctx context.Context, i domain.Invoice, e domain.Event) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(
		ctx,
		`INSERT INTO invoices(id,description,amount,chain_id,token_address,recipient_address,expires_at,status,version,created_at,updated_at) VALUES($1,$2,$3::text::numeric,$4,$5,$6,$7,$8,$9,$10,$11)`,
		i.ID,
		i.Description,
		i.Amount,
		i.ChainID,
		i.TokenAddress,
		i.RecipientAddress,
		i.ExpiresAt,
		i.Status,
		i.Version,
		i.CreatedAt,
		i.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if err = appendEvent(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Save(ctx context.Context, i domain.Invoice, version int64, e domain.Event) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(
		ctx,
		`UPDATE invoices SET description=$2,amount=$3::text::numeric,chain_id=$4,token_address=$5,recipient_address=$6,expires_at=$7,status=$8,version=$9,updated_at=$10,issued_at=$11,cancelled_at=$12,paid_at=$13 WHERE id=$1 AND version=$14`,
		i.ID,
		i.Description,
		i.Amount,
		i.ChainID,
		i.TokenAddress,
		i.RecipientAddress,
		i.ExpiresAt,
		i.Status,
		i.Version,
		i.UpdatedAt,
		i.IssuedAt,
		i.CancelledAt,
		i.PaidAt,
		version,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	if err = appendEvent(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Delete(ctx context.Context, id string, version int64) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	i, err := scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM invoices WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return err
	}
	if i.Status != domain.Draft || i.Version != version {
		return domain.ErrConflict
	}
	if _, err = tx.Exec(ctx, "DELETE FROM invoices WHERE id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Events(ctx context.Context, id string) ([]domain.Event, error) {
	rows, err := p.pool.Query(
		ctx,
		"SELECT id,invoice_id,type,occurred_at,version FROM invoice_events WHERE invoice_id=$1 ORDER BY version",
		id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var e domain.Event
		if err = rows.Scan(&e.ID, &e.InvoiceID, &e.Type, &e.OccurredAt, &e.Version); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) Migrate(ctx context.Context) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(73481921)"); err != nil {
		return err
	}
	if _, err = tx.Exec(
		ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`,
	); err != nil {
		return err
	}
	files, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	for _, f := range files {
		sql, err := migrations.Files.ReadFile(f.Name())
		if err != nil {
			return err
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(sql))
		var existing string
		err = tx.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE name=$1", f.Name()).
			Scan(&existing)
		if err == nil {
			if existing != sum {
				return fmt.Errorf("migration %s was modified after application", f.Name())
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", f.Name(), err)
		}
		if _, err = tx.Exec(
			ctx,
			"INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)",
			f.Name(),
			sum,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
