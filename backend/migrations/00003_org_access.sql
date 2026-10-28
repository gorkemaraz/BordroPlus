-- +goose Up
-- Task 5: salary storage, manager delegation and idempotent POSTs.

-- Salary history. The amount (kuruş) is AES-256-GCM encrypted by the API;
-- the database never sees it in clear. The ciphertext is bound to
-- (employee_id, valid_from) through GCM additional data, so copying one
-- row's ciphertext into another row fails decryption.
CREATE TABLE employee_salaries (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id  bigint NOT NULL REFERENCES employees (id),
    valid_from   date   NOT NULL,
    amount_enc   bytea  NOT NULL CHECK (octet_length(amount_enc) BETWEEN 29 AND 128),
    key_version  smallint NOT NULL CHECK (key_version > 0),
    created_by   bigint REFERENCES users (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (employee_id, valid_from)
);

-- A manager hands their approval scope to a delegate for a period, e.g.
-- while on leave. Delegations of one manager may not overlap in time.
CREATE TABLE manager_delegations (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    manager_employee_id  bigint NOT NULL REFERENCES employees (id),
    delegate_employee_id bigint NOT NULL REFERENCES employees (id),
    valid_during         daterange NOT NULL CHECK (NOT isempty(valid_during) AND lower_inc(valid_during)),
    reason               text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
    created_by           bigint NOT NULL REFERENCES users (id),
    created_at           timestamptz NOT NULL DEFAULT now(),
    revoked_at           timestamptz,
    CHECK (manager_employee_id <> delegate_employee_id),
    EXCLUDE USING gist (manager_employee_id WITH =, valid_during WITH &&) WHERE (revoked_at IS NULL)
);
CREATE INDEX manager_delegations_delegate_idx ON manager_delegations USING gist (delegate_employee_id, valid_during)
    WHERE revoked_at IS NULL;

-- Stored responses for POSTs carrying an Idempotency-Key. A repeat with the
-- same key and body replays the stored response; a different body is rejected.
CREATE TABLE idempotency_keys (
    user_id       bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key           text   NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
    request_hash  bytea  NOT NULL,
    status_code   integer,
    response_body bytea,
    created_at    timestamptz NOT NULL DEFAULT now(),
    completed_at  timestamptz,
    PRIMARY KEY (user_id, key)
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE idempotency_keys;
DROP TABLE manager_delegations;
DROP TABLE employee_salaries;
