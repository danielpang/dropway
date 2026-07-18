// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

// Structural guards for the tenant-scoping invariant PR #100 established. Two of
// its bugs compiled cleanly and slipped past `go build`: a query gained an
// `org_id = $n` predicate (so its generated Params struct got an OrgID field),
// but a hand-written call site omitted OrgID, leaving it at the zero value "".
// For the audit-log and AI-transcript reads that is a hard uuid error (org_id is
// a NOT NULL uuid column); for a text column it would silently return zero rows.
// The Go compiler cannot catch an omitted struct field, so these tests catch the
// class at plain `go test` time (no database needed).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseGoFiles parses every non-test .go file matching glob into AST files,
// failing the test on any parse error.
func parseGoFiles(t *testing.T, fset *token.FileSet, glob string) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob(glob)
	if err != nil {
		t.Fatalf("glob %s: %v", glob, err)
	}
	var out []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		out = append(out, f)
	}
	return out
}

// orgParamStructs parses the generated db package and returns the set of
// <Name>Params struct types that carry an OrgID field (i.e. the query filters or
// writes on org_id, so every caller MUST supply it).
func orgParamStructs(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, f := range parseGoFiles(t, token.NewFileSet(), filepath.Join("db", "*.go")) {
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || !strings.HasSuffix(ts.Name.Name, "Params") {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				for _, nm := range fld.Names {
					if nm.Name == "OrgID" {
						out[ts.Name.Name] = true
					}
				}
			}
			return true
		})
	}
	return out
}

// TestOrgScopedParamsCallSitesSetOrgID asserts every db.<Name>Params{...} literal
// constructed in the store package sets OrgID when the struct has that field. This
// is the exact class of the audit-log (ListAuditLog) and AI-transcript
// (ListAIMessages) bugs from the PR #100 review.
func TestOrgScopedParamsCallSitesSetOrgID(t *testing.T) {
	orgStructs := orgParamStructs(t)
	if len(orgStructs) == 0 {
		t.Fatal("found no *Params structs with an OrgID field; the test is not exercising anything")
	}

	fset := token.NewFileSet()
	checked := 0
	for _, f := range parseGoFiles(t, fset, "*.go") {
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// Match `db.<Name>Params{...}`.
			sel, ok := cl.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "db" {
				return true
			}
			name := sel.Sel.Name
			if !orgStructs[name] {
				return true
			}
			checked++
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "OrgID" {
					return true // OrgID is set — good.
				}
			}
			pos := fset.Position(cl.Pos())
			t.Errorf("%s:%d: db.%s{...} is constructed without setting OrgID — the query filters/writes on org_id, so this call runs with org_id=\"\" (a silent cross-tenant scoping bug). Pass OrgID: t.OrgID.",
				filepath.Base(pos.Filename), pos.Line, name)
			return true
		})
	}
	if checked == 0 {
		t.Fatal("found no db.*Params{...} literals with an OrgID field to check; the AST match is broken")
	}
}

// TestListSkillsFolderSubqueryOrgScoped is a regression test for the third PR #100
// review finding: ListSkills' folder-membership EXISTS subquery must constrain the
// skill_folder_items / skill_folders rows by org (as its MCP twin does), so a
// folder-filtered listing stays correct even when RLS is inert under a bypass role.
func TestListSkillsFolderSubqueryOrgScoped(t *testing.T) {
	sql := generatedQuerySQL(t, "listSkills")
	// The subquery joins fi (skill_folder_items) and f (skill_folders); both must
	// be org-constrained.
	for _, want := range []string{"fi.org_id", "f.org_id"} {
		if !strings.Contains(sql, want) {
			t.Errorf("ListSkills query does not constrain the folder subquery by %s; a folder-filtered listing could match another org's folder rows when RLS is inert", want)
		}
	}
}

// generatedQuerySQL extracts the raw SQL string of a named sqlc query constant
// (e.g. `const listSkills = ` + "`...`") from the generated db package.
func generatedQuerySQL(t *testing.T, constName string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("db", "query.sql.go"))
	if err != nil {
		t.Fatalf("read generated query.sql.go: %v", err)
	}
	marker := "const " + constName + " = `"
	_, rest, found := strings.Cut(string(src), marker)
	if !found {
		t.Fatalf("could not find generated query constant %q", constName)
	}
	sql, _, found := strings.Cut(rest, "`")
	if !found {
		t.Fatalf("unterminated SQL literal for %q", constName)
	}
	return sql
}
