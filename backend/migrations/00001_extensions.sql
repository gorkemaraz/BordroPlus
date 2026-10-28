-- +goose Up
-- ltree:      organisation tree paths (manager sub-tree queries)
-- pgcrypto:   salary field encryption, gen_random_uuid
-- btree_gist: exclusion constraints on validity ranges (rule parameters, delegations)
-- citext:     case-insensitive e-mail addresses
CREATE EXTENSION IF NOT EXISTS ltree;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS citext;

-- +goose Down
DROP EXTENSION IF EXISTS citext;
DROP EXTENSION IF EXISTS btree_gist;
DROP EXTENSION IF EXISTS pgcrypto;
DROP EXTENSION IF EXISTS ltree;
