// Command testboundarylint checks the declared levels of repository Go tests.
package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type diagnostic struct {
	path    string
	line    int
	message string
}

const (
	integrationLevel = "integration"
	e2eLevel         = "e2e"
	sharedLevel      = "shared"
)

func main() {
	if err := checkTree(".", os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func checkTree(root string, output io.Writer) error {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer directory.Close()

	violations := 0
	err = fs.WalkDir(directory.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != "." && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readErr := directory.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, problem := range checkFile(filepath.Join(root, path), source) {
			if _, writeErr := fmt.Fprintf(
				output,
				"%s:%d: %s\n",
				problem.path,
				problem.line,
				problem.message,
			); writeErr != nil {
				return writeErr
			}
			violations++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if violations != 0 {
		return fmt.Errorf("test boundary lint: %d violations", violations)
	}
	return nil
}

func ignoredDirectory(name string) bool {
	return strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata"
}

func checkFile(path string, source []byte) []diagnostic {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, parser.ParseComments)
	if err != nil {
		return []diagnostic{{path: path, line: 1, message: "parse Go source: " + err.Error()}}
	}
	level, problems := checkConstraints(path, positions, file)
	report := func(line int, message string) {
		problems = append(problems, diagnostic{path: path, line: line, message: message})
	}
	if ast.IsGenerated(file) {
		return problems
	}
	behavior := false
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && isBehavior(function.Name.Name) {
			behavior = true
			break
		}
	}
	suffixLevel := ""
	switch {
	case strings.HasSuffix(path, "_integration_test.go"):
		suffixLevel = integrationLevel
	case strings.HasSuffix(path, "_e2e_test.go"):
		suffixLevel = e2eLevel
	}
	if !behavior {
		return problems
	}
	if level != suffixLevel || level == sharedLevel {
		report(1, "behavior filename and build constraint must declare the same single level (unit is untagged)")
	}
	return problems
}

func checkConstraints(path string, positions *token.FileSet, file *ast.File) (string, []diagnostic) {
	var problems []diagnostic
	report := func(line int, message string) {
		problems = append(problems, diagnostic{path: path, line: line, message: message})
	}
	level := ""
	constraintCount := 0
	for _, comment := range buildComments(file) {
		line := positions.Position(comment.Pos()).Line
		expression, err := constraint.Parse(comment.Text)
		if err != nil {
			report(line, "invalid build constraint: "+err.Error())
			continue
		}
		if comment.Pos() > file.Package {
			report(line, "build constraint must precede package declaration")
		}
		declared, valid := constraintLevel(expression)
		if !valid {
			report(line, "expected integration, e2e, or integration || e2e constraint")
		}
		if constraint.IsGoBuild(comment.Text) {
			constraintCount++
			level = declared
		} else if level != declared {
			report(line, "legacy build constraint must match //go:build")
		}
	}
	if constraintCount > 1 {
		report(1, "expected at most one //go:build constraint")
	}
	return level, problems
}

func buildComments(file *ast.File) []*ast.Comment {
	var comments []*ast.Comment
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if constraint.IsGoBuild(comment.Text) || constraint.IsPlusBuild(comment.Text) {
				comments = append(comments, comment)
			}
		}
	}
	return comments
}

func isBehavior(name string) bool {
	if name == "TestMain" {
		return false
	}
	for _, prefix := range []string{"Test", "Benchmark", "Example", "Fuzz"} {
		rest, found := strings.CutPrefix(name, prefix)
		if found && (rest == "" || !unicode.IsLower([]rune(rest)[0])) {
			return true
		}
	}
	return false
}

func constraintLevel(expression constraint.Expr) (string, bool) {
	if !hasOnlyLevelTags(expression) {
		return "", false
	}
	values := [4]bool{}
	for index := range values {
		values[index] = expression.Eval(func(tag string) bool {
			switch tag {
			case integrationLevel:
				return index&1 != 0
			case e2eLevel:
				return index&2 != 0
			default:
				return false
			}
		})
	}
	switch values {
	case [4]bool{false, true, false, true}:
		return integrationLevel, true
	case [4]bool{false, false, true, true}:
		return e2eLevel, true
	case [4]bool{false, true, true, true}:
		return sharedLevel, true
	default:
		return "", false
	}
}

func hasOnlyLevelTags(expression constraint.Expr) bool {
	switch expression := expression.(type) {
	case *constraint.TagExpr:
		return expression.Tag == integrationLevel || expression.Tag == e2eLevel
	case *constraint.NotExpr:
		return hasOnlyLevelTags(expression.X)
	case *constraint.AndExpr:
		return hasOnlyLevelTags(expression.X) && hasOnlyLevelTags(expression.Y)
	case *constraint.OrExpr:
		return hasOnlyLevelTags(expression.X) && hasOnlyLevelTags(expression.Y)
	default:
		return false
	}
}
