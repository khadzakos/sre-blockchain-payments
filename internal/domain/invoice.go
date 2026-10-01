package domain

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid input")
	ErrConflict    = errors.New("invoice state or version conflict")
	ErrNotFound    = errors.New("invoice not found")
	addressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	amountPattern  = regexp.MustCompile(`^[1-9][0-9]{0,77}$`)
)

type Status string

const (
	Draft     Status = "draft"
	Open      Status = "open"
	Paid      Status = "paid"
	Expired   Status = "expired"
	Cancelled Status = "cancelled"
)

type Terms struct {
	Description      string    `json:"description"`
	Amount           string    `json:"amount"`
	ChainID          int64     `json:"chain_id"`
	TokenAddress     string    `json:"token_address"`
	RecipientAddress string    `json:"recipient_address"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func (t *Terms) Validate(now time.Time) error {
	t.Description = strings.TrimSpace(t.Description)
	if t.Description == "" || utf8.RuneCountInString(t.Description) > 500 {
		return fmt.Errorf("%w: description must contain 1–500 characters", ErrInvalid)
	}
	n, ok := new(big.Int).SetString(t.Amount, 10)
	if !amountPattern.MatchString(t.Amount) || !ok || n.BitLen() > 256 {
		return fmt.Errorf("%w: amount must be a positive uint256 integer string", ErrInvalid)
	}
	if t.ChainID <= 0 {
		return fmt.Errorf("%w: chain_id must be positive", ErrInvalid)
	}
	for _, a := range []string{t.TokenAddress, t.RecipientAddress} {
		if !addressPattern.MatchString(a) ||
			strings.EqualFold(a, "0x0000000000000000000000000000000000000000") {
			return fmt.Errorf("%w: nonzero EVM address required", ErrInvalid)
		}
	}
	if !t.ExpiresAt.After(now) {
		return fmt.Errorf("%w: expires_at must be in the future", ErrInvalid)
	}
	t.ExpiresAt = t.ExpiresAt.UTC()
	t.TokenAddress = strings.ToLower(t.TokenAddress)
	t.RecipientAddress = strings.ToLower(t.RecipientAddress)
	return nil
}

type Invoice struct {
	ID string `json:"id"`
	Terms
	Status      Status     `json:"status"`
	Version     int64      `json:"version"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	IssuedAt    *time.Time `json:"issued_at"`
	CancelledAt *time.Time `json:"cancelled_at"`
	PaidAt      *time.Time `json:"paid_at"`
}

func NewInvoice(id string, terms Terms, now time.Time) (Invoice, error) {
	if err := terms.Validate(now); err != nil {
		return Invoice{}, err
	}
	return Invoice{ID: id, Terms: terms, Status: Draft, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func (i *Invoice) Edit(terms Terms, now time.Time) error {
	if i.Status != Draft {
		return ErrConflict
	}
	if err := terms.Validate(now); err != nil {
		return err
	}
	i.Terms = terms
	i.UpdatedAt = now
	return nil
}

func (i *Invoice) Issue(now time.Time) error {
	if i.Status != Draft {
		return ErrConflict
	}
	if err := i.Validate(now); err != nil {
		return err
	}
	i.Status, i.IssuedAt, i.UpdatedAt = Open, &now, now
	return nil
}

func (i *Invoice) Cancel(now time.Time) error {
	if i.Status != Open || !now.Before(i.ExpiresAt) {
		return ErrConflict
	}
	i.Status, i.CancelledAt, i.UpdatedAt = Cancelled, &now, now
	return nil
}

func (i *Invoice) Expire(now time.Time) error {
	if i.Status != Open || now.Before(i.ExpiresAt) {
		return ErrConflict
	}
	i.Status, i.UpdatedAt = Expired, now
	return nil
}

type Event struct {
	ID         int64     `json:"id"`
	InvoiceID  string    `json:"invoice_id"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	Version    int64     `json:"version"`
}
