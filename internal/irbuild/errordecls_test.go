package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
)

// errorDeclsSource holds a type error inside one function, broken, and
// nowhere else, so the front end refuses it and can name the declaration
// that holds the error.
const errorDeclsSource = `fn broken() -> Int
    "text"
end

fn main()
    _ = broken()
end
`

// errorDeclsWant is the front end's diagnostic for errorDeclsSource.
const errorDeclsWant = "return type mismatch: expected Int, got String"

// Every entry that answers a Program and an error answers a nil Program
// beside a front-end error, so a caller that tests the Program before the
// error never takes a refused program for a checked one. Only a caller
// that sets frontend.Config.ErrorDecls gets the program beside the error,
// with its ErrorDecls naming the function the error is in.
func TestAnalyze_ARefusedProgramIsNilUnlessErrorDeclsAsks(t *testing.T) {
	path := writeTemp(t, errorDeclsSource)
	ordinary := []struct {
		name    string
		analyze func() (*Program, error)
	}{
		{"Analyze", func() (*Program, error) { return Analyze(path) }},
		{"AnalyzeFile", func() (*Program, error) { return AnalyzeFile(path, frontend.Config{}) }},
		{"AnalyzeFileSource", func() (*Program, error) { return AnalyzeFileSource(path, errorDeclsSource, frontend.Config{}) }},
		{"AnalyzeVirtual", func() (*Program, error) { return AnalyzeVirtual("main", errorDeclsSource, frontend.Config{}) }},
		{"AnalyzeSource", func() (*Program, error) { return AnalyzeSource("main", errorDeclsSource) }},
	}
	for _, tc := range ordinary {
		p, err := tc.analyze()
		requireFrontEndRejection(t, err, tc.name+" of a type error in a function", errorDeclsWant)
		if p != nil {
			t.Errorf("%s answers a Program beside the front end's error; want nil", tc.name)
		}
	}

	asked := frontend.Config{ErrorDecls: true}
	editor := []struct {
		name    string
		analyze func() (*Program, error)
	}{
		{"AnalyzeFile", func() (*Program, error) { return AnalyzeFile(path, asked) }},
		{"AnalyzeFileSource", func() (*Program, error) { return AnalyzeFileSource(path, errorDeclsSource, asked) }},
		{"AnalyzeVirtual", func() (*Program, error) { return AnalyzeVirtual("main", errorDeclsSource, asked) }},
	}
	for _, tc := range editor {
		p, err := tc.analyze()
		requireFrontEndRejection(t, err, tc.name+" with ErrorDecls of a type error in a function", errorDeclsWant)
		if p == nil {
			t.Errorf("%s with ErrorDecls answers no Program beside the error", tc.name)
			continue
		}
		var names []string
		for decl := range p.ErrorDecls {
			fd, ok := decl.(*ast.FuncDef)
			if !ok {
				t.Errorf("%s with ErrorDecls names a %T", tc.name, decl)
				continue
			}
			names = append(names, fd.Name)
		}
		if len(names) != 1 || names[0] != "broken" {
			t.Errorf("%s with ErrorDecls names %v; want [broken]", tc.name, names)
		}
	}
}
