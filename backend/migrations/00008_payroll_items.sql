-- +goose Up
-- F-5: bonuses, salary advances and loans recovered in instalments.
CREATE TABLE payroll_items (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id   bigint NOT NULL REFERENCES employees (id),
    kind          text   NOT NULL CHECK (kind IN ('bonus', 'advance', 'loan')),
    label         text   NOT NULL CHECK (length(btrim(label)) BETWEEN 1 AND 100),
    amount        bigint NOT NULL CHECK (amount > 0),          -- total, kuruş
    instalments   integer NOT NULL DEFAULT 1 CHECK (instalments BETWEEN 1 AND 36),
    start_period  date   NOT NULL CHECK (extract(day FROM start_period) = 1),
    created_by    bigint NOT NULL REFERENCES users (id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    cancelled_by  bigint REFERENCES users (id),
    cancelled_at  timestamptz,
    -- Bonuses and advances are one-off; only loans are spread.
    CHECK (kind = 'loan' OR instalments = 1),
    CHECK ((cancelled_at IS NULL) = (cancelled_by IS NULL))
);
CREATE INDEX payroll_items_employee_idx ON payroll_items (employee_id, start_period) WHERE cancelled_at IS NULL;

-- +goose Down
DROP TABLE payroll_items;
