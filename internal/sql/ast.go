package sql

// Statement describes syntax only. Type selects select/update/delete;
// Columns belong to SELECT and Assignments belong to UPDATE.
type Statement struct {
	Type        string       `json:"type"`
	Columns     []Column     `json:"columns,omitempty"`
	Table       string       `json:"table"`
	Where       Expression   `json:"where,omitempty"`
	Assignments []Assignment `json:"assignments,omitempty"`
}

type Assignment struct {
	Column   string     `json:"column"`
	Value    Expression `json:"value"`
	Position Position   `json:"-"`
}

// Column is either a named column or an explicit star, expanded during binding.
type Column struct {
	Type     string   `json:"type"`
	Name     string   `json:"name,omitempty"`
	Position Position `json:"-"`
}
