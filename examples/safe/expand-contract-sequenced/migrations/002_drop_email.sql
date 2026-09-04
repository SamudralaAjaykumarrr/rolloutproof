-- Contract step: sequenced strictly after full rollout completion of the
-- version (api@v2) that stopped reading the old column.
ALTER TABLE users DROP COLUMN email;
