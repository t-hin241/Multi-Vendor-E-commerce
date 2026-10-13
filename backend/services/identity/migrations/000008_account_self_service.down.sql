-- Forward-only security migration: rolling back must retain revocations,
-- confirmed addresses and outstanding delivery obligations.
DO $$ BEGIN RAISE EXCEPTION 'AF-12 is forward-only; disable the feature flag and use a compatible binary'; END $$;
