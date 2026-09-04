-- Contract step shipped in the SAME rollout as the expand step, with no
-- confirmation that every consumer of the old shape has migrated off it.
ALTER TABLE users DROP COLUMN email;
