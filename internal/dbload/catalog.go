package dbload

import (
	"context"
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
		queries[relationDevices+"/"+name] = text
		coverage = append(
			coverage,
			QueryCoverage{
				Owner:  relationDevices,
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
	data, err := querySourceAt(root, revision, path)
	if err != nil {
		return nil, err
	}
	return parseQueryConstants(path, data)
}

func querySourceAt(root, revision, path string) ([]byte, error) {
	if revision == "" {
		return os.ReadFile(path)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	//nolint:gosec // revision comes from build metadata and the path from a fixed glob.
	command := exec.CommandContext(context.Background(), "git", "show", revision+":"+filepath.ToSlash(relative))
	command.Dir = root
	return command.Output()
}

func parseQueryConstants(path string, data []byte) (map[string]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, decl := range f.Decls {
		if err = addQueryConstants(result, decl); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func addQueryConstants(result map[string]string, decl ast.Decl) error {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.CONST {
		return nil
	}
	for _, spec := range gen.Specs {
		if err := addQueryConstant(result, spec); err != nil {
			return err
		}
	}
	return nil
}

func addQueryConstant(result map[string]string, spec ast.Spec) error {
	value, isValue := spec.(*ast.ValueSpec)
	if !isValue || len(value.Values) != 1 {
		return nil
	}
	literal, isLiteral := value.Values[0].(*ast.BasicLit)
	if !isLiteral || literal.Kind != token.STRING {
		return nil
	}
	text, err := strconv.Unquote(literal.Value)
	if err != nil {
		return err
	}
	fields := strings.Fields(text)
	switch {
	case len(fields) > 3 && fields[0] == "--" && fields[1] == "name:":
		result[fields[2]] = text
	case len(value.Names) == 1 && value.Names[0].Name == "getEntityStateSnapshotSQL":
		result["GetEntityStateSnapshot"] = text
	}
	return nil
}
