package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

type sessionColumn struct {
	name         string
	kind         string
	required     int
	defaultValue sql.NullString
	primary      int
}

type schemaReader interface {
	Query(string, ...any) (*sql.Rows, error)
}

func sessionColumns(reader schemaReader, table string) ([]sessionColumn, error) {
	rows, err := reader.Query(`SELECT name,type,"notnull",dflt_value,pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []sessionColumn
	for rows.Next() {
		var column sessionColumn
		if err := rows.Scan(&column.name, &column.kind, &column.required, &column.defaultValue, &column.primary); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func repairSessionColumns(tx *sql.Tx) error {
	reference, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer reference.Close()
	reference.SetMaxOpenConns(1)
	if _, err := reference.Exec(sessionSchema); err != nil {
		return err
	}
	for _, table := range []string{"schema_version", "sessions", "messages", "prompt_queue"} {
		expected, err := sessionColumns(reference, table)
		if err != nil {
			return err
		}
		actual, err := sessionColumns(tx, table)
		if err != nil {
			return err
		}
		if len(actual) == 0 {
			continue
		}
		present := map[string]bool{}
		for _, column := range actual {
			present[strings.ToLower(column.name)] = true
		}
		for _, column := range expected {
			if present[strings.ToLower(column.name)] {
				continue
			}
			if column.primary != 0 || column.required != 0 && !column.defaultValue.Valid {
				return fmt.Errorf("cannot safely repair required column %s.%s", table, column.name)
			}
			definition := `"` + column.name + `" ` + column.kind
			if column.defaultValue.Valid && column.defaultValue.String == "unixepoch()" && (column.name == "created_at" || column.name == "updated_at") {
				if _, err := tx.Exec(`ALTER TABLE "` + table + `" ADD COLUMN "` + column.name + `" INTEGER DEFAULT 0`); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE "` + table + `" SET "` + column.name + `"=unixepoch() WHERE "` + column.name + `"=0`); err != nil {
					return err
				}
				if _, err := tx.Exec(`CREATE TRIGGER "` + table + `_fill_` + column.name + `" AFTER INSERT ON "` + table + `" WHEN NEW."` + column.name + `"=0 BEGIN UPDATE "` + table + `" SET "` + column.name + `"=unixepoch() WHERE rowid=NEW.rowid; END`); err != nil {
					return err
				}
				continue
			}
			if column.required != 0 {
				definition += " NOT NULL"
			}
			if column.defaultValue.Valid {
				definition += " DEFAULT (" + column.defaultValue.String + ")"
			}
			if _, err := tx.Exec(`ALTER TABLE "` + table + `" ADD COLUMN ` + definition); err != nil {
				return fmt.Errorf("repair %s.%s: %w", table, column.name, err)
			}
		}
	}
	return nil
}
