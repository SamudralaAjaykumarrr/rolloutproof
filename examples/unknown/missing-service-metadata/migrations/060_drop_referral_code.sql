-- Destructive, but no contract metadata exists for api at any version:
-- RolloutProof cannot determine whether this is safe or not.
ALTER TABLE users DROP COLUMN referral_code;
