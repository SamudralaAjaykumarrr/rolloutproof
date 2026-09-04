-- Bare rename, no prior expand step: functionally a simultaneous drop of
-- "email" and add of "email_address", with no compatibility period.
ALTER TABLE users RENAME COLUMN email TO email_address;
