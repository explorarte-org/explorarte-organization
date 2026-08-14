-- Reasigna a explorarte_app el ownership de todo objeto en el schema
-- public que haya quedado propiedad del rol que ejecutó pg_restore
-- (típicamente explorarte_admin, ya que pg_restore --no-owner asigna
-- ownership al rol que conecta, no al owner original del dump).
--
-- Correr como explorarte_admin (o cualquier superusuario/owner de base)
-- inmediatamente después de pg_restore, ANTES del paso 5 de
-- RUNBOOK-restore-against-existing-database.md (ALTER SCHEMA public
-- OWNER TO / GRANT USAGE), que corrige el schema contenedor mismo -- este
-- script solo corrige los objetos DENTRO de él.
--
-- Secuencias ligadas a una columna IDENTITY/serial se excluyen
-- deliberadamente: Postgres transfiere su ownership automáticamente
-- cuando se transfiere el de la tabla que las posee, y un ALTER SEQUENCE
-- ... OWNER TO explícito sobre una de ellas falla con
-- "cannot change owner of sequence ... it is linked to table ...".
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relowner = 'explorarte_admin'::regrole LOOP
    IF r.relkind = 'r' THEN EXECUTE format('ALTER TABLE public.%I OWNER TO explorarte_app', r.relname);
    ELSIF r.relkind = 'v' THEN EXECUTE format('ALTER VIEW public.%I OWNER TO explorarte_app', r.relname);
    ELSIF r.relkind = 'S' THEN
      IF NOT EXISTS (
        SELECT 1 FROM pg_depend d
        WHERE d.objid = (quote_ident('public')||'.'||quote_ident(r.relname))::regclass
          AND d.deptype = 'i'
      ) THEN
        EXECUTE format('ALTER SEQUENCE public.%I OWNER TO explorarte_app', r.relname);
      END IF;
    END IF;
  END LOOP;
  FOR r IN SELECT p.oid::regprocedure::text AS sig FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proowner = 'explorarte_admin'::regrole LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO explorarte_app', r.sig);
  END LOOP;
END $$;
