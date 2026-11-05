package dbload

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// Catalog reads exact generated SQL constants, including SQLC's final bind positions.
// Application workloads still call production repositories/generated methods.
func Catalog(root string) ([]QueryCoverage, map[string]string, error) {
	return catalogAt(root, "")
}

func catalogAt(root, revision string) ([]QueryCoverage, map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "internal/modules/*/sqlite/dbsqlc/*.sql.go"))
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no production queries found under %s", root)
	}
	queries := make(map[string]string)
	var coverage []QueryCoverage
	for _, path := range files {
		owner := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
		items, loadErr := queryConstantsAt(root, revision, path)
		if loadErr != nil {
			return nil, nil, loadErr
		}
		for name, text := range items {
			key := owner + "/" + name
			queries[key] = text
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil, nil, relErr
			}
			coverage = append(
				coverage,
				QueryCoverage{
					Owner:  owner,
					Name:   name,
					Source: rel,
					Hash:   hashBytes([]byte(text)),
					Status: "uncovered",
					Reason: "no completed timed case exercises this statement",
				},
			)
		}
	}
	path := filepath.Join(root, "internal/modules/devices/sqlite/entity_state_snapshot.go")
	items, err := queryConstantsAt(root, revision, path)
	if err != nil {
		return nil, nil, err
	}
	for name, text := range items {
		queries["devices/"+name] = text
		coverage = append(
			coverage,
			QueryCoverage{
				Owner:  "devices",
				Name:   name,
				Source: "internal/modules/devices/sqlite/entity_state_snapshot.go",
				Hash:   hashBytes([]byte(text)),
				Status: "uncovered",
				Reason: "no completed timed case exercises this statement",
			},
		)
	}
	sort.Slice(coverage, func(i, j int) bool {
		return coverage[i].Owner+"/"+coverage[i].Name < coverage[j].Owner+"/"+coverage[j].Name
	})
	return coverage, queries, nil
}

func queryConstantsAt(root, revision, path string) (map[string]string, error) {
	var data []byte
	var err error
	if revision == "" {
		data, err = os.ReadFile(path)
	} else {
		var relative string
		relative, err = filepath.Rel(root, path)
		if err == nil {
			command := exec.Command("git", "show", revision+":"+filepath.ToSlash(relative))
			command.Dir = root
			data, err = command.Output()
		}
	}
	if err != nil {
		return nil, err
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue || len(value.Values) != 1 {
				continue
			}
			literal, isLiteral := value.Values[0].(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				continue
			}
			text, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				return nil, unquoteErr
			}
			fields := strings.Fields(text)
			if len(fields) > 3 && fields[0] == "--" && fields[1] == "name:" {
				result[fields[2]] = text
			} else if len(value.Names) == 1 && value.Names[0].Name == "getEntityStateSnapshotSQL" {
				result["GetEntityStateSnapshot"] = text
			}
		}
	}
	return result, nil
}
