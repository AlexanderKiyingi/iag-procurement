-- Procurement API schema (aligned with procurement-web/lib/types.ts)

CREATE TABLE IF NOT EXISTS vendors (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    logo TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    contact TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    phone TEXT NOT NULL DEFAULT '',
    country TEXT NOT NULL DEFAULT '',
    terms TEXT NOT NULL DEFAULT '',
    rating DOUBLE PRECISION NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'Active',
    total_spend NUMERIC NOT NULL DEFAULT 0,
    open_pos INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS items (
    id TEXT PRIMARY KEY,
    sku TEXT NOT NULL,
    name TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    uom TEXT NOT NULL DEFAULT '',
    stock NUMERIC NOT NULL DEFAULT 0,
    reorder NUMERIC NOT NULL DEFAULT 0,
    last_price NUMERIC NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    preferred_vendor_id TEXT REFERENCES vendors (id)
);

CREATE TABLE IF NOT EXISTS budgets (
    id TEXT PRIMARY KEY,
    code TEXT NOT NULL,
    period TEXT NOT NULL DEFAULT '',
    allocated NUMERIC NOT NULL DEFAULT 0,
    committed NUMERIC NOT NULL DEFAULT 0,
    spent NUMERIC NOT NULL DEFAULT 0,
    remaining NUMERIC NOT NULL DEFAULT 0,
    dept TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS requisitions (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    dept TEXT NOT NULL DEFAULT '',
    requester TEXT NOT NULL DEFAULT '',
    priority TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    created_at DATE,
    needed_by DATE,
    total NUMERIC NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    budget_id TEXT REFERENCES budgets (id)
);

CREATE TABLE IF NOT EXISTS rfqs (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT '',
    due_date DATE,
    created_at DATE,
    winner_vendor_id TEXT REFERENCES vendors (id),
    invited_vendor_ids TEXT[] NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS purchase_orders (
    id TEXT PRIMARY KEY,
    vendor_id TEXT NOT NULL REFERENCES vendors (id),
    title TEXT NOT NULL,
    total NUMERIC NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    status TEXT NOT NULL DEFAULT '',
    created_at DATE,
    expected_date DATE,
    budget_id TEXT REFERENCES budgets (id)
);

CREATE TABLE IF NOT EXISTS po_lines (
    id SERIAL PRIMARY KEY,
    po_id TEXT NOT NULL REFERENCES purchase_orders (id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES items (id),
    qty NUMERIC NOT NULL,
    unit_price NUMERIC NOT NULL
);

CREATE TABLE IF NOT EXISTS grns (
    id TEXT PRIMARY KEY,
    po_id TEXT,
    vendor_id TEXT NOT NULL REFERENCES vendors (id),
    received_date DATE,
    received_by TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS invoices (
    id TEXT PRIMARY KEY,
    invoice_no TEXT,
    vendor_id TEXT NOT NULL REFERENCES vendors (id),
    po_id TEXT,
    amount NUMERIC NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    status TEXT NOT NULL DEFAULT '',
    match_status TEXT NOT NULL DEFAULT '',
    invoice_date DATE
);

CREATE TABLE IF NOT EXISTS contracts (
    id TEXT PRIMARY KEY,
    vendor_id TEXT NOT NULL REFERENCES vendors (id),
    title TEXT NOT NULL,
    start_date DATE,
    end_date DATE,
    value NUMERIC NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    status TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS payments (
    id TEXT PRIMARY KEY,
    invoice_id TEXT NOT NULL REFERENCES invoices (id),
    vendor_id TEXT NOT NULL REFERENCES vendors (id),
    amount NUMERIC NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    pay_date DATE,
    method TEXT NOT NULL DEFAULT '',
    reference TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    initiated_by TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS audit_entries (
    id SERIAL PRIMARY KEY,
    ts TIMESTAMP NOT NULL DEFAULT NOW(),
    username TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_po_lines_po ON po_lines (po_id);
CREATE INDEX IF NOT EXISTS idx_requisitions_budget ON requisitions (budget_id);
CREATE INDEX IF NOT EXISTS idx_invoices_vendor ON invoices (vendor_id);
