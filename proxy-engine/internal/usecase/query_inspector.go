package usecase

import (
	"strings"
	"github.com/wasilibs/go-pgquery/parser"
)

// QueryInspector defines the interface for analyzing queries.
type QueryInspector interface {
	IsDestructive(query string) (bool, error)
}

// PGQueryInspector implements QueryInspector using go-pgquery.
type PGQueryInspector struct{}

// NewPGQueryInspector creates a new PGQueryInspector.
func NewPGQueryInspector() *PGQueryInspector {
	return &PGQueryInspector{}
}

// IsDestructive parses the query and returns true if it contains destructive statements.
func (i *PGQueryInspector) IsDestructive(query string) (bool, error) {
	treeJSON, err := parser.ParseToJSON(query)
	if err != nil {
		return false, err
	}

	isDestructive := strings.Contains(treeJSON, `"DropStmt":{`) ||
		strings.Contains(treeJSON, `"DeleteStmt":{`) ||
		strings.Contains(treeJSON, `"TruncateStmt":{`) ||
		strings.Contains(treeJSON, `"DropdbStmt":{`) ||
		strings.Contains(treeJSON, `"AlterTableStmt":{`) ||
		strings.Contains(treeJSON, `"RenameStmt":{`)

	return isDestructive, nil
}
