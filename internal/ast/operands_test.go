package ast

import (
	"sort"
	"strings"
	"testing"
)

// Every field of every AST node type that can hold a child has a class in
// operandFields, and the table names no field the AST lacks. A node type or
// a field added to the AST fails here, by name, until someone decides
// whether the node always evaluates it: one left to a default would be read
// as leaving, or lifted, from a position that does not always run it.
func TestEveryChildFieldHasAnOperandClass(t *testing.T) {
	inAST := map[string]bool{}
	var missing []string
	counts := map[string]int{}
	NodeChildFields(func(node, field string) {
		key := node + "." + field
		inAST[key] = true
		rule, classified := operandFields[node][field]
		switch {
		case !classified:
			missing = append(missing, key)
		case rule.of != nil && rule.class != 0, rule.of == nil && rule.class == 0:
			t.Errorf("%s: a rule is one class or one function", key)
		case rule.of != nil:
			counts["by the node"]++
		case rule.class == operandNow:
			counts["operandNow"]++
		case rule.class == operandLater:
			counts["operandLater"]++
		case rule.class == operandNames:
			counts["operandNames"]++
		case rule.class == operandScoped:
			counts["operandScoped"]++
		default:
			t.Errorf("%s: class %d is none of the classes", key, rule.class)
		}
	})
	if len(inAST) < 150 {
		t.Fatalf("NodeChildFields lists only %d fields", len(inAST))
	}
	if len(missing) > 0 {
		t.Errorf("no operand class for %d child fields; add each to operandFields (operands.go) as operandNow only if the node always evaluates it before its own value is read:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	var stale []string
	for node, fields := range operandFields {
		for field := range fields {
			if !inAST[node+"."+field] {
				stale = append(stale, node+"."+field)
			}
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("operandFields classifies fields the AST does not have:\n  %s", strings.Join(stale, "\n  "))
	}
	t.Logf("%d child fields: %v", len(inAST), counts)
}
