-- Must be rejected: no migration rollback support.
ALTER TABLE public.users DROP COLUMN username;
