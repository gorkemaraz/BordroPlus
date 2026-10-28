-- +goose Up
-- State of the data simulator: whether the bulk history is loaded (and with
-- which seed) and the heartbeat of its live mode.
CREATE TABLE simulator_state (
    id             smallint PRIMARY KEY CHECK (id = 1),
    seed           bigint  NOT NULL,
    employees      integer NOT NULL,
    history_from   date    NOT NULL,
    history_to     date    NOT NULL,
    loaded_at      timestamptz NOT NULL DEFAULT now(),
    load_ms        integer NOT NULL,
    punches_loaded bigint  NOT NULL,
    last_tick_at   timestamptz,
    live_punches   bigint  NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE simulator_state;
