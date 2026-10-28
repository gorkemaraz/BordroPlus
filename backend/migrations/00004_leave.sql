-- +goose Up
-- Task 1: annual leave. Amounts are integer half-days (1 day = 2) so half-day
-- leave and half-day holidays need no fractions.

-- Age matters: workers aged <= 18 or >= 50 get at least 20 days.
ALTER TABLE employees ADD COLUMN birth_date date
    CHECK (birth_date IS NULL OR (birth_date < hire_date AND birth_date > '1900-01-01'));

-- Public holidays. half_day = only the afternoon is off (arife, 28 October).
CREATE TABLE public_holidays (
    holiday_date date PRIMARY KEY,
    name         text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 100),
    half_day     boolean NOT NULL DEFAULT false
);

-- Official holidays 2024-2026 (Resmî Gazete / Diyanet). For 2027 only the
-- fixed-date national holidays are listed: religious holiday dates are added
-- through POST /api/v1/leave/holidays once officially announced.
INSERT INTO public_holidays (holiday_date, name, half_day) VALUES
    ('2024-01-01', 'Yılbaşı', false),
    ('2024-04-09', 'Ramazan Bayramı arifesi', true),
    ('2024-04-10', 'Ramazan Bayramı 1. gün', false),
    ('2024-04-11', 'Ramazan Bayramı 2. gün', false),
    ('2024-04-12', 'Ramazan Bayramı 3. gün', false),
    ('2024-04-23', 'Ulusal Egemenlik ve Çocuk Bayramı', false),
    ('2024-05-01', 'Emek ve Dayanışma Günü', false),
    ('2024-05-19', 'Atatürk''ü Anma, Gençlik ve Spor Bayramı', false),
    ('2024-06-15', 'Kurban Bayramı arifesi', true),
    ('2024-06-16', 'Kurban Bayramı 1. gün', false),
    ('2024-06-17', 'Kurban Bayramı 2. gün', false),
    ('2024-06-18', 'Kurban Bayramı 3. gün', false),
    ('2024-06-19', 'Kurban Bayramı 4. gün', false),
    ('2024-07-15', 'Demokrasi ve Millî Birlik Günü', false),
    ('2024-08-30', 'Zafer Bayramı', false),
    ('2024-10-28', 'Cumhuriyet Bayramı arifesi', true),
    ('2024-10-29', 'Cumhuriyet Bayramı', false),

    ('2025-01-01', 'Yılbaşı', false),
    ('2025-03-29', 'Ramazan Bayramı arifesi', true),
    ('2025-03-30', 'Ramazan Bayramı 1. gün', false),
    ('2025-03-31', 'Ramazan Bayramı 2. gün', false),
    ('2025-04-01', 'Ramazan Bayramı 3. gün', false),
    ('2025-04-23', 'Ulusal Egemenlik ve Çocuk Bayramı', false),
    ('2025-05-01', 'Emek ve Dayanışma Günü', false),
    ('2025-05-19', 'Atatürk''ü Anma, Gençlik ve Spor Bayramı', false),
    ('2025-06-05', 'Kurban Bayramı arifesi', true),
    ('2025-06-06', 'Kurban Bayramı 1. gün', false),
    ('2025-06-07', 'Kurban Bayramı 2. gün', false),
    ('2025-06-08', 'Kurban Bayramı 3. gün', false),
    ('2025-06-09', 'Kurban Bayramı 4. gün', false),
    ('2025-07-15', 'Demokrasi ve Millî Birlik Günü', false),
    ('2025-08-30', 'Zafer Bayramı', false),
    ('2025-10-28', 'Cumhuriyet Bayramı arifesi', true),
    ('2025-10-29', 'Cumhuriyet Bayramı', false),

    ('2026-01-01', 'Yılbaşı', false),
    ('2026-03-19', 'Ramazan Bayramı arifesi', true),
    ('2026-03-20', 'Ramazan Bayramı 1. gün', false),
    ('2026-03-21', 'Ramazan Bayramı 2. gün', false),
    ('2026-03-22', 'Ramazan Bayramı 3. gün', false),
    ('2026-04-23', 'Ulusal Egemenlik ve Çocuk Bayramı', false),
    ('2026-05-01', 'Emek ve Dayanışma Günü', false),
    ('2026-05-19', 'Atatürk''ü Anma, Gençlik ve Spor Bayramı', false),
    ('2026-05-26', 'Kurban Bayramı arifesi', true),
    ('2026-05-27', 'Kurban Bayramı 1. gün', false),
    ('2026-05-28', 'Kurban Bayramı 2. gün', false),
    ('2026-05-29', 'Kurban Bayramı 3. gün', false),
    ('2026-05-30', 'Kurban Bayramı 4. gün', false),
    ('2026-07-15', 'Demokrasi ve Millî Birlik Günü', false),
    ('2026-08-30', 'Zafer Bayramı', false),
    ('2026-10-28', 'Cumhuriyet Bayramı arifesi', true),
    ('2026-10-29', 'Cumhuriyet Bayramı', false),

    ('2027-01-01', 'Yılbaşı', false),
    ('2027-04-23', 'Ulusal Egemenlik ve Çocuk Bayramı', false),
    ('2027-05-01', 'Emek ve Dayanışma Günü', false),
    ('2027-05-19', 'Atatürk''ü Anma, Gençlik ve Spor Bayramı', false),
    ('2027-07-15', 'Demokrasi ve Millî Birlik Günü', false),
    ('2027-08-30', 'Zafer Bayramı', false),
    ('2027-10-28', 'Cumhuriyet Bayramı arifesi', true),
    ('2027-10-29', 'Cumhuriyet Bayramı', false);

CREATE TABLE leave_requests (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id   bigint NOT NULL REFERENCES employees (id),
    leave_type    text   NOT NULL CHECK (leave_type IN ('annual', 'sick', 'unpaid', 'excuse')),
    start_date    date   NOT NULL,
    end_date      date   NOT NULL,
    half_day      text   NOT NULL DEFAULT '' CHECK (half_day IN ('', 'morning', 'afternoon')),
    -- Working-time cost in half-days, recomputed at approval.
    half_days     integer NOT NULL CHECK (half_days >= 0),
    status        text   NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    reason        text   NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
    decision_note text   NOT NULL DEFAULT '' CHECK (length(decision_note) <= 500),
    requested_by  bigint NOT NULL REFERENCES users (id),
    decided_by    bigint REFERENCES users (id),
    decided_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    -- A sick report may overlap annual leave (its days are refunded);
    -- anything else may not overlap another active request.
    overlap_group text GENERATED ALWAYS AS (CASE WHEN leave_type = 'sick' THEN 'sick' ELSE 'absence' END) STORED,
    CHECK (end_date >= start_date AND end_date - start_date <= 366),
    CHECK (half_day = '' OR start_date = end_date),
    EXCLUDE USING gist (employee_id WITH =, overlap_group WITH =, daterange(start_date, end_date, '[]') WITH &&)
        WHERE (status IN ('pending', 'approved'))
);
CREATE INDEX leave_requests_employee_idx ON leave_requests (employee_id, start_date DESC);
CREATE INDEX leave_requests_pending_idx ON leave_requests (id) WHERE status = 'pending';
CREATE TRIGGER leave_requests_updated BEFORE UPDATE ON leave_requests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Current balance per employee: the row approvals lock (SELECT ... FOR
-- UPDATE) so concurrent approvals serialise. The CHECK is the last line of
-- defence: whatever the code does, the balance cannot go negative.
CREATE TABLE leave_accounts (
    employee_id       bigint PRIMARY KEY REFERENCES employees (id),
    balance_half_days integer NOT NULL DEFAULT 0 CHECK (balance_half_days >= 0),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Every balance movement. balance_half_days always equals the sum of an
-- employee's ledger rows (verified by tests).
CREATE TABLE leave_ledger (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id        bigint NOT NULL REFERENCES employees (id),
    kind               text   NOT NULL CHECK (kind IN ('accrual', 'usage', 'sick_refund', 'reversal', 'adjustment')),
    half_days          integer NOT NULL CHECK (half_days <> 0),
    effective_date     date   NOT NULL,
    leave_request_id   bigint REFERENCES leave_requests (id),
    related_request_id bigint REFERENCES leave_requests (id),
    note               text   NOT NULL DEFAULT '',
    created_by         bigint REFERENCES users (id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind IN ('accrual', 'sick_refund', 'reversal')) = (half_days > 0) OR kind = 'adjustment'),
    CHECK (kind = 'accrual' OR kind = 'adjustment' OR leave_request_id IS NOT NULL)
);
CREATE INDEX leave_ledger_employee_idx ON leave_ledger (employee_id, effective_date, id);
-- One accrual per anniversary, one refund per (annual leave, sick report) pair.
CREATE UNIQUE INDEX leave_ledger_accrual_uq ON leave_ledger (employee_id, effective_date) WHERE kind = 'accrual';
CREATE UNIQUE INDEX leave_ledger_refund_uq ON leave_ledger (leave_request_id, related_request_id) WHERE kind = 'sick_refund';
CREATE UNIQUE INDEX leave_ledger_once_uq ON leave_ledger (leave_request_id, kind) WHERE kind IN ('usage', 'reversal');

-- +goose StatementBegin
CREATE FUNCTION reject_modification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only (% rejected)', TG_TABLE_NAME, TG_OP USING ERRCODE = 'insufficient_privilege';
END $$;
-- +goose StatementEnd
CREATE TRIGGER leave_ledger_append_only BEFORE UPDATE OR DELETE ON leave_ledger
    FOR EACH ROW EXECUTE FUNCTION reject_modification();

-- +goose Down
DROP TABLE leave_ledger;
DROP FUNCTION reject_modification();
DROP TABLE leave_accounts;
DROP TABLE leave_requests;
DROP TABLE public_holidays;
ALTER TABLE employees DROP COLUMN birth_date;
