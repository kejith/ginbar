-- Test-only schema expansion. NOT an embedded/production migration.
ALTER TABLE public.users ADD COLUMN compatibility_probe_note text;
