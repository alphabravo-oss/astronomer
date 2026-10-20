// Package testdb is the integration-test substrate (Technology Selection
// Guide ch.07): one PostgreSQL 17 container per test process started through
// testcontainers-go, a template database migrated once with the production
// goose migrator, and a fresh clone of that template for every test through
// pgtestdb. Redis comes from the testcontainers redis module.
//
// Every file that uses it carries `//go:build integration`; `make test` never
// needs Docker, `make test-integration` does.
package testdb
