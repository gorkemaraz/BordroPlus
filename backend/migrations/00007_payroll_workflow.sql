-- +goose Up
-- Task 4: payroll period workflow, immutability of frozen periods and
-- prior-period corrections.

CREATE TABLE payroll_periods (
    period         date PRIMARY KEY CHECK (extract(day FROM period) = 1),
    status         text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'review', 'approved', 'closed')),
    rule_version   text REFERENCES payroll_rule_sets (version),
    employee_count integer NOT NULL DEFAULT 0,
    totals         jsonb  NOT NULL DEFAULT '{}',
    digest         bytea,          -- hash of every result's input hash, in employee order
    last_run_at    timestamptz,
    last_run_ms    integer,
    last_run_failures integer NOT NULL DEFAULT 0,
    submitted_by   bigint REFERENCES users (id),
    submitted_at   timestamptz,
    approved_by    bigint REFERENCES users (id),
    approved_at    timestamptz,
    closed_by      bigint REFERENCES users (id),
    closed_at      timestamptz,
    review_note    text NOT NULL DEFAULT '' CHECK (length(review_note) <= 500),
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (status <> 'closed' OR closed_at IS NOT NULL),
    CHECK (status NOT IN ('approved', 'closed') OR approved_at IS NOT NULL)
);

-- A closed period never changes and no period is ever deleted.
-- +goose StatementBegin
CREATE FUNCTION payroll_periods_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'payroll periods cannot be deleted' USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    IF OLD.status = 'closed' THEN
        RAISE EXCEPTION 'payroll period % is closed', OLD.period USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER payroll_periods_guard BEFORE UPDATE OR DELETE ON payroll_periods
    FOR EACH ROW EXECUTE FUNCTION payroll_periods_guard();

-- Results of a period under review, approved or closed are frozen, whatever
-- the application does.
-- +goose StatementBegin
CREATE FUNCTION payroll_frozen_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    p  date;
    st text;
BEGIN
    IF TG_TABLE_NAME = 'payroll_adjustments' THEN
        p := CASE WHEN TG_OP = 'DELETE' THEN OLD.target_period ELSE NEW.target_period END;
    ELSE
        p := CASE WHEN TG_OP = 'DELETE' THEN OLD.period ELSE NEW.period END;
    END IF;
    SELECT status INTO st FROM payroll_periods WHERE period = p;
    IF st IN ('review', 'approved', 'closed') THEN
        RAISE EXCEPTION 'payroll period % is %: % rejected', p, st, TG_OP USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END $$;
-- +goose StatementEnd
CREATE TRIGGER payroll_results_frozen BEFORE INSERT OR UPDATE OR DELETE ON payroll_results
    FOR EACH ROW EXECUTE FUNCTION payroll_frozen_guard();

-- Differences found for closed months, paid (or recovered) in a later
-- period: "geçmiş dönem düzeltmesi".
CREATE TABLE payroll_adjustments (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id   bigint NOT NULL REFERENCES employees (id),
    source_period date   NOT NULL,
    target_period date   NOT NULL,
    amount        bigint NOT NULL CHECK (amount <> 0),
    reason        text   NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (employee_id, source_period, target_period),
    CHECK (source_period < target_period)
);
CREATE INDEX payroll_adjustments_source_idx ON payroll_adjustments (employee_id, source_period);
CREATE TRIGGER payroll_adjustments_frozen BEFORE INSERT OR UPDATE OR DELETE ON payroll_adjustments
    FOR EACH ROW EXECUTE FUNCTION payroll_frozen_guard();

-- Change tracking for prior-period corrections.
CREATE INDEX employee_salaries_created_idx ON employee_salaries (created_at);
CREATE INDEX leave_requests_decided_idx ON leave_requests (decided_at) WHERE leave_type = 'unpaid' AND status = 'approved';

-- +goose Down
DROP INDEX leave_requests_decided_idx;
DROP INDEX employee_salaries_created_idx;
DROP TABLE payroll_adjustments;
DROP TRIGGER payroll_results_frozen ON payroll_results;
DROP FUNCTION payroll_frozen_guard();
DROP TABLE payroll_periods;
DROP FUNCTION payroll_periods_guard();
