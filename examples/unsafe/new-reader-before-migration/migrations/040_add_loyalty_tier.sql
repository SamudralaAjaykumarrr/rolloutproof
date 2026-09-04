-- Scheduled to run only after the rollout completes — api@v2 reads this
-- column before it exists (SC-UNSAFE-004).
ALTER TABLE users ADD COLUMN loyalty_tier text;
