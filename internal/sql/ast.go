package sql

// SelectStatement describes syntax only; table and column names are not bound.
type SelectStatement struct {
	Type    string     `json:"type"`
	Columns []Column   `json:"columns"`
	Table   string     `json:"table"`
	Where   Expression `json:"where,omitempty"`
}

// Column is either a named column or an explicit star, expanded during binding.
type Column struct {
	Type     string   `json:"type"`
	Name     string   `json:"name,omitempty"`
	Position Position `json:"-"`
}
