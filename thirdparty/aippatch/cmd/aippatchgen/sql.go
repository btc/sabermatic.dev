package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

type Schema struct {
	tables map[string]*Table
}

type Table struct {
	Name     string
	cols     map[string]*Column
	colOrder []string
}

type Column struct {
	Name    string
	Type    string // normalized lowercase ("text", "integer", "smallint", ...)
	NotNull bool
}

func newSchema() *Schema { return &Schema{tables: map[string]*Table{}} }

func (s *Schema) Table(name string) (*Table, bool) {
	t, ok := s.tables[name]
	return t, ok
}

func (t *Table) Column(name string) (*Column, bool) {
	c, ok := t.cols[name]
	return c, ok
}

func (t *Table) Columns() []*Column {
	out := make([]*Column, 0, len(t.colOrder))
	for _, n := range t.colOrder {
		out = append(out, t.cols[n])
	}
	return out
}

func loadSchema(dir string) (*Schema, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("aippatchgen: read migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	s := newSchema()
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if err := s.applySQL(string(body)); err != nil {
			return nil, fmt.Errorf("aippatchgen: %s: %w", name, err)
		}
	}
	return s, nil
}

func (s *Schema) applySQL(sqlText string) error {
	res, err := pg_query.Parse(sqlText)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	for _, raw := range res.Stmts {
		stmt := raw.GetStmt()
		switch n := stmt.GetNode().(type) {
		case *pg_query.Node_CreateStmt:
			s.applyCreateTable(n.CreateStmt)
		case *pg_query.Node_AlterTableStmt:
			s.applyAlterTable(n.AlterTableStmt)
		case *pg_query.Node_DropStmt:
			s.applyDrop(n.DropStmt)
		case *pg_query.Node_RenameStmt:
			// pg_query_go/v6 represents `ALTER TABLE … RENAME COLUMN x TO y` as a
			// top-level RenameStmt, not as an AlterTableCmd subcommand.
			s.applyRename(n.RenameStmt)
		default:
			// ignore: indexes, constraints, etc.
		}
	}
	return nil
}

// applyRename handles `ALTER TABLE … RENAME COLUMN`, which pg_query_go/v6
// parses as Node_RenameStmt at the top level of the parse tree.
func (s *Schema) applyRename(r *pg_query.RenameStmt) {
	if r.GetRenameType() != pg_query.ObjectType_OBJECT_COLUMN {
		return
	}
	tbl, ok := s.tables[r.GetRelation().GetRelname()]
	if !ok {
		return
	}
	oldName := r.GetSubname()
	newName := r.GetNewname()
	c, ok := tbl.cols[oldName]
	if !ok {
		return
	}
	c.Name = newName
	tbl.cols[newName] = c
	delete(tbl.cols, oldName)
	for i, n := range tbl.colOrder {
		if n == oldName {
			tbl.colOrder[i] = newName
			break
		}
	}
}

func (s *Schema) applyCreateTable(c *pg_query.CreateStmt) {
	tbl := &Table{Name: c.Relation.GetRelname(), cols: map[string]*Column{}}
	for _, el := range c.TableElts {
		if cd := el.GetColumnDef(); cd != nil {
			col := columnFromDef(cd)
			tbl.cols[col.Name] = col
			tbl.colOrder = append(tbl.colOrder, col.Name)
		}
	}
	s.tables[tbl.Name] = tbl
}

func (s *Schema) applyAlterTable(a *pg_query.AlterTableStmt) {
	tbl, ok := s.tables[a.Relation.GetRelname()]
	if !ok {
		return
	}
	for _, raw := range a.Cmds {
		cmd := raw.GetAlterTableCmd()
		if cmd == nil {
			continue
		}
		switch cmd.GetSubtype() {
		case pg_query.AlterTableType_AT_AddColumn:
			cd := cmd.GetDef().GetColumnDef()
			if cd != nil {
				col := columnFromDef(cd)
				tbl.cols[col.Name] = col
				tbl.colOrder = append(tbl.colOrder, col.Name)
			}
		case pg_query.AlterTableType_AT_DropColumn:
			delete(tbl.cols, cmd.GetName())
			for i, n := range tbl.colOrder {
				if n == cmd.GetName() {
					tbl.colOrder = append(tbl.colOrder[:i], tbl.colOrder[i+1:]...)
					break
				}
			}
		case pg_query.AlterTableType_AT_AlterColumnType:
			if c, ok := tbl.cols[cmd.GetName()]; ok {
				if cd := cmd.GetDef().GetColumnDef(); cd != nil {
					c.Type = typeName(cd)
				}
			}
		case pg_query.AlterTableType_AT_SetNotNull:
			if c, ok := tbl.cols[cmd.GetName()]; ok {
				c.NotNull = true
			}
		case pg_query.AlterTableType_AT_DropNotNull:
			if c, ok := tbl.cols[cmd.GetName()]; ok {
				c.NotNull = false
			}
		default:
			// ignored: ADD CONSTRAINT, DROP DEFAULT, etc.
			// NOTE: RENAME COLUMN is NOT handled here. pg_query_go/v6 parses
			// `ALTER TABLE … RENAME COLUMN` as a top-level Node_RenameStmt,
			// not as an AlterTableCmd subtype — there is no
			// AT_RenameColumn constant. See applyRename above.
		}
	}
}

func (s *Schema) applyDrop(d *pg_query.DropStmt) {
	if d.GetRemoveType() != pg_query.ObjectType_OBJECT_TABLE {
		return
	}
	for _, raw := range d.Objects {
		list := raw.GetList()
		if list == nil {
			continue
		}
		// last name segment is the table name
		items := list.GetItems()
		if len(items) > 0 {
			name := items[len(items)-1].GetString_().GetSval()
			delete(s.tables, name)
		}
	}
}

func columnFromDef(cd *pg_query.ColumnDef) *Column {
	col := &Column{Name: cd.GetColname(), Type: typeName(cd)}
	for _, raw := range cd.Constraints {
		c := raw.GetConstraint()
		if c == nil {
			continue
		}
		switch c.GetContype() {
		case pg_query.ConstrType_CONSTR_NOTNULL,
			pg_query.ConstrType_CONSTR_PRIMARY:
			col.NotNull = true
		}
	}
	return col
}

func typeName(cd *pg_query.ColumnDef) string {
	tn := cd.GetTypeName()
	if tn == nil {
		return ""
	}
	parts := tn.GetNames()
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1].GetString_().GetSval()
	return strings.ToLower(last)
}
