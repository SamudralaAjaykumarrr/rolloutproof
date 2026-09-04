-- legacy_flag was already unused by any live version (SC-SAFE-005).
ALTER TABLE users DROP COLUMN legacy_flag;
