-- +goose Up
-- Task 2: clock punches, correction proposals and approved monthly timesheets.

-- Raw punches from devices, the web clock or HR. Never updated or deleted:
-- corrections add punches (source = 'correction') or mark one ignored.
CREATE TABLE time_punches (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id   bigint NOT NULL REFERENCES employees (id),
    punched_at    timestamptz NOT NULL,
    direction     text NOT NULL CHECK (direction IN ('in', 'out')),
    source        text NOT NULL CHECK (source IN ('device', 'web', 'manual', 'correction')),
    device_id     text CHECK (length(device_id) <= 64),
    correction_id bigint,
    created_by    bigint REFERENCES users (id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((source = 'correction') = (correction_id IS NOT NULL))
);
CREATE INDEX time_punches_employee_time_idx ON time_punches (employee_id, punched_at);
-- A device retrying the same upload must not create a second punch.
CREATE UNIQUE INDEX time_punches_device_retry_uq ON time_punches (employee_id, punched_at, direction) WHERE source = 'device';
CREATE TRIGGER time_punches_append_only BEFORE UPDATE OR DELETE ON time_punches
    FOR EACH ROW EXECUTE FUNCTION reject_modification();

-- Automatically generated fixes for inconsistent punches. Nothing is applied
-- until a manager or HR approves; suggestion_key identifies the anomaly
-- (drop:<punch>, out:<in punch>, in:<out punch>) so re-analysis is idempotent.
CREATE TABLE punch_corrections (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id      bigint NOT NULL REFERENCES employees (id),
    suggestion_key   text   NOT NULL,
    kind             text   NOT NULL CHECK (kind IN ('drop_duplicate', 'add_missing_out', 'add_missing_in')),
    target_punch_id  bigint NOT NULL REFERENCES time_punches (id),
    proposed_at      timestamptz,
    work_date        date   NOT NULL,
    reason           text   NOT NULL,
    status           text   NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed', 'approved', 'rejected', 'obsolete')),
    applied_at       timestamptz,
    decision_note    text   NOT NULL DEFAULT '' CHECK (length(decision_note) <= 500),
    decided_by       bigint REFERENCES users (id),
    decided_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (employee_id, suggestion_key),
    CHECK ((kind = 'drop_duplicate') = (proposed_at IS NULL)),
    CHECK (status <> 'approved' OR kind = 'drop_duplicate' OR applied_at IS NOT NULL)
);
CREATE INDEX punch_corrections_open_idx ON punch_corrections (employee_id, work_date) WHERE status = 'proposed';
CREATE INDEX punch_corrections_target_idx ON punch_corrections (target_punch_id) WHERE kind = 'drop_duplicate' AND status = 'approved';
ALTER TABLE time_punches ADD CONSTRAINT time_punches_correction_fk FOREIGN KEY (correction_id) REFERENCES punch_corrections (id);

-- An approved month is frozen: totals are snapshotted for payroll and no
-- punch or correction may change it afterwards.
CREATE TABLE timesheet_periods (
    employee_id  bigint NOT NULL REFERENCES employees (id),
    month        date   NOT NULL CHECK (extract(day FROM month) = 1),
    rule_version text   NOT NULL,
    totals       jsonb  NOT NULL,
    approved_by  bigint NOT NULL REFERENCES users (id),
    approved_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (employee_id, month)
);
CREATE INDEX timesheet_periods_month_idx ON timesheet_periods (month);

-- +goose Down
DROP TABLE timesheet_periods;
ALTER TABLE time_punches DROP CONSTRAINT time_punches_correction_fk;
DROP TABLE punch_corrections;
DROP TABLE time_punches;
