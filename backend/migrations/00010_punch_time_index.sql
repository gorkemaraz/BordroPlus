-- +goose Up
-- "Punches today" on the dashboard filters by time alone; the
-- (employee_id, punched_at) index cannot serve it, so every status request
-- scanned all punches (~100 ms, 3 parallel workers). Found by the k6 run.
CREATE INDEX time_punches_punched_at_idx ON time_punches (punched_at);

-- +goose Down
DROP INDEX time_punches_punched_at_idx;
