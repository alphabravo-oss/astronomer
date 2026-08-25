BEGIN;

CREATE TABLE migration_interrupt_before_sleep (
  id integer PRIMARY KEY
);

SELECT pg_sleep(30)
FROM migration_test_control
WHERE should_sleep;

CREATE TABLE migration_interrupt_after_sleep (
  id integer PRIMARY KEY
);

COMMIT;
