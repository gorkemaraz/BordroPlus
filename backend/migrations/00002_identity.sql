-- +goose Up
-- Identity core: organisation tree, employee card, login accounts, refresh
-- tokens and the append-only audit log. Payroll, leave and timesheet tables
-- arrive in later migrations and reference these.

-- updated_at maintenance shared by every mutable table.
-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- Organisation units form a tree. path is a materialised ltree path of unit
-- codes (e.g. genel_mudurluk.bilgi_teknolojileri.yazilim) maintained by
-- triggers, so "everything under unit X" is an indexed `path <@ X.path` query.
CREATE TABLE org_units (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    parent_id  bigint REFERENCES org_units (id),
    code       text   NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9_]{0,62}$'),
    name       text   NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    path       ltree  NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (parent_id IS DISTINCT FROM id)
);
CREATE INDEX org_units_path_gist ON org_units USING gist (path);
CREATE INDEX org_units_parent_idx ON org_units (parent_id);
-- Exactly one root.
CREATE UNIQUE INDEX org_units_single_root ON org_units ((true)) WHERE parent_id IS NULL;

-- +goose StatementBegin
CREATE FUNCTION org_units_set_path() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    parent_path ltree;
BEGIN
    IF NEW.parent_id IS NULL THEN
        NEW.path := text2ltree(NEW.code);
        RETURN NEW;
    END IF;
    SELECT path INTO parent_path FROM org_units WHERE id = NEW.parent_id;
    IF parent_path IS NULL THEN
        RAISE EXCEPTION 'parent org unit % does not exist', NEW.parent_id USING ERRCODE = 'foreign_key_violation';
    END IF;
    -- Moving a unit under its own subtree would create a cycle.
    IF TG_OP = 'UPDATE' AND parent_path <@ OLD.path THEN
        RAISE EXCEPTION 'org unit % cannot be moved under its own subtree', NEW.id USING ERRCODE = 'check_violation';
    END IF;
    NEW.path := parent_path || text2ltree(NEW.code);
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- After a move/rename, rewrite the paths of every descendant in one statement.
-- +goose StatementBegin
CREATE FUNCTION org_units_cascade_path() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE org_units
       SET path = NEW.path || subpath(path, nlevel(OLD.path))
     WHERE path <@ OLD.path AND id <> NEW.id;
    RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER org_units_path_ins BEFORE INSERT ON org_units
    FOR EACH ROW EXECUTE FUNCTION org_units_set_path();
-- The cascade only SETs path, so it does not re-fire these column triggers.
CREATE TRIGGER org_units_path_upd BEFORE UPDATE OF parent_id, code ON org_units
    FOR EACH ROW EXECUTE FUNCTION org_units_set_path();
CREATE TRIGGER org_units_path_cascade AFTER UPDATE OF parent_id, code ON org_units
    FOR EACH ROW WHEN (OLD.path IS DISTINCT FROM NEW.path) EXECUTE FUNCTION org_units_cascade_path();
CREATE TRIGGER org_units_updated BEFORE UPDATE ON org_units
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Employee card. Salary and identity numbers are added (encrypted) with the
-- payroll schema; this table holds only non-sensitive master data.
CREATE TABLE employees (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_no      text   NOT NULL UNIQUE CHECK (employee_no ~ '^[A-Z0-9-]{3,20}$'),
    first_name       text   NOT NULL CHECK (length(btrim(first_name)) BETWEEN 1 AND 100),
    last_name        text   NOT NULL CHECK (length(btrim(last_name)) BETWEEN 1 AND 100),
    title            text   NOT NULL DEFAULT '' CHECK (length(title) <= 200),
    org_unit_id      bigint NOT NULL REFERENCES org_units (id),
    hire_date        date   NOT NULL,
    termination_date date,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (termination_date IS NULL OR termination_date >= hire_date)
);
CREATE INDEX employees_org_unit_idx ON employees (org_unit_id);
CREATE TRIGGER employees_updated BEFORE UPDATE ON employees
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A unit's manager. A manager sees every employee whose unit path is under
-- the managed unit's path.
ALTER TABLE org_units ADD COLUMN manager_id bigint REFERENCES employees (id);
CREATE INDEX org_units_manager_idx ON org_units (manager_id) WHERE manager_id IS NOT NULL;

-- Login accounts. One role per account; the permission matrix lives in code
-- (internal/auth/rbac.go) so it is versioned and unit-tested.
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email         citext NOT NULL UNIQUE CHECK (length(email) <= 254 AND email ~ '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'),
    password_hash text   NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
    role          text   NOT NULL CHECK (role IN ('employee', 'manager', 'hr', 'payroll', 'accounting', 'auditor')),
    employee_id   bigint UNIQUE REFERENCES employees (id),
    is_active     boolean NOT NULL DEFAULT true,
    failed_logins integer NOT NULL DEFAULT 0 CHECK (failed_logins >= 0),
    locked_until  timestamptz,
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    -- Self-service roles are meaningless without an employee record.
    CHECK (role NOT IN ('employee', 'manager') OR employee_id IS NOT NULL)
);
CREATE TRIGGER users_updated BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Opaque refresh tokens; only their SHA-256 is stored. Every login starts a
-- family; each refresh marks the presented token used and issues a successor
-- in the same family. Presenting a used token again means it was stolen, and
-- the whole family is revoked.
CREATE TABLE refresh_tokens (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id  uuid   NOT NULL,
    token_hash bytea  NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at    timestamptz,
    revoked_at timestamptz,
    CHECK (expires_at > created_at)
);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_active_idx ON refresh_tokens (user_id) WHERE revoked_at IS NULL;

-- Append-only audit trail: who did what to which entity, with old/new values.
CREATE TABLE audit_log (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    actor_user_id bigint REFERENCES users (id),
    actor_role    text,
    action        text NOT NULL CHECK (action ~ '^[a-z_]+(\.[a-z_]+)*$'),
    entity_type   text NOT NULL,
    entity_id     text,
    old_value     jsonb,
    new_value     jsonb,
    request_id    text,
    ip            inet
);
CREATE INDEX audit_log_entity_idx ON audit_log (entity_type, entity_id, id DESC);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_user_id, id DESC);

-- +goose StatementBegin
CREATE FUNCTION audit_log_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only (% rejected)', TG_OP USING ERRCODE = 'insufficient_privilege';
END $$;
-- +goose StatementEnd

CREATE TRIGGER audit_log_no_update BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_append_only();

-- +goose Down
DROP TABLE audit_log;
DROP FUNCTION audit_log_append_only();
DROP TABLE refresh_tokens;
DROP TABLE users;
ALTER TABLE org_units DROP COLUMN manager_id;
DROP TABLE employees;
DROP TABLE org_units;
DROP FUNCTION org_units_cascade_path();
DROP FUNCTION org_units_set_path();
DROP FUNCTION set_updated_at();
