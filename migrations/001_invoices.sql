CREATE TABLE invoices (
    id text PRIMARY KEY,
    description text NOT NULL,
    amount numeric(78,0) NOT NULL,
    chain_id bigint NOT NULL,
    token_address text NOT NULL CHECK (token_address ~ '^0x[0-9a-f]{40}$'),
    recipient_address text NOT NULL CHECK (recipient_address ~ '^0x[0-9a-f]{40}$'),
    expires_at timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('draft','open','paid','expired','cancelled')),
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    issued_at timestamptz,
    cancelled_at timestamptz,
    paid_at timestamptz,
    CHECK (status = 'draft' OR issued_at IS NOT NULL),
    CHECK (status <> 'paid' OR paid_at IS NOT NULL),
    CHECK (status <> 'cancelled' OR cancelled_at IS NOT NULL)
);
CREATE INDEX invoices_list ON invoices(created_at DESC,id DESC);
CREATE INDEX invoices_due ON invoices(expires_at) WHERE status='open';
CREATE TABLE invoice_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    invoice_id text NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    type text NOT NULL,
    occurred_at timestamptz NOT NULL,
    version bigint NOT NULL,
    UNIQUE(invoice_id,version)
);
