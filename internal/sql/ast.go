package sql

// Statement describes syntax only. Columns belong to SELECT/INSERT,
// Assignments to UPDATE, and Values to a single INSERT tuple.
type Statement struct {
	Type        string       `json:"type"`
	Columns     []Column     `json:"columns,omitempty"`
	Table       string       `json:"table"`
	TableQuoted bool         `json:"table_quoted,omitempty"`
	Where       Expression   `json:"where,omitempty"`
	Assignments []Assignment `json:"assignments,omitempty"`
	Values      []Expression `json:"values,omitempty"`
}

type Assignment struct {
	Column   string     `json:"column"`
	Quoted   bool       `json:"quoted,omitempty"`
	Value    Expression `json:"value"`
	Position Position   `json:"-"`
}

// Column is either a named column or an explicit star, expanded during binding.
type Column struct {
	Type     string   `json:"type"`
	Name     string   `json:"name,omitempty"`
	Quoted   bool     `json:"quoted,omitempty"`
	Alias    string   `json:"alias,omitempty"`
	Position Position `json:"-"`
}
