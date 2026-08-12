package migrate

import (
	"strings"
	"testing"
)

func TestSchemaSQL_DefinesRequiredTables(t *testing.T) {
	sql := SchemaSQL()
	if sql == "" {
		t.Fatal("embedded schema empty")
	}
	for _, table := range RequiredTables() {
		if !SchemaDefinesTable(table) {
			t.Errorf("schema missing table %q", table)
		}
	}
	// baseline game seed
	if !strings.Contains(sql, "252490") {
		t.Error("schema should seed Rust appid 252490")
	}
	if !strings.Contains(sql, "730") {
		t.Error("schema should seed CS2 appid 730 for multi-game model")
	}
	if !strings.Contains(sql, "market_hash_name") {
		t.Error("items must use market_hash_name")
	}
	if MigrationVersion() == "" {
		t.Error("empty migration version")
	}
}

func TestSplitSQL_ProducesStatements(t *testing.T) {
	stmts := splitSQL(SchemaSQL())
	if len(stmts) < 5 {
		t.Fatalf("expected multiple statements, got %d", len(stmts))
	}
	// first meaningful statement should create a table
	joined := strings.ToUpper(strings.Join(stmts, "\n"))
	if !strings.Contains(joined, "CREATE TABLE") {
		t.Fatal("no CREATE TABLE in split statements")
	}
}

func TestApply_EmptyDSN(t *testing.T) {
	err := Apply(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty dsn")
	}
}
