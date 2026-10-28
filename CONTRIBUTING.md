# Contributing

The Wolf Community source is licensed under the Apache License 2.0. Unless you
state otherwise, any contribution you submit is licensed under the same terms
(Apache-2.0 section 5).

`main` requires a pull request and the GitHub Actions check named `test`.
Admins cannot bypass that check; do not push commits straight to `main`.

Tests that touch the database run on PostgreSQL. Start a throwaway server
(`docker run -d --rm -p 55434:5432 -e POSTGRES_PASSWORD=test postgres:18-alpine`)
and export `WOLF_TEST_POSTGRES_DSN=postgres://postgres:test@localhost:55434/postgres?sslmode=disable`;
without it those tests skip locally (CI fails instead). Use `db.NewTestPostgres(t)`
for a private, migrated database per test; it is safe under `t.Parallel`.

Do not copy files from `wolf-enterprise` into this repository.
