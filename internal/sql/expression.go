package sql

// Expression nodes describe syntax only; evaluation belongs to the engine.
type Expression interface {
	expressionNode()
}

type Literal struct {
	Type     string   `json:"type"`
	Value    any      `json:"value"`
	Position Position `json:"-"`
}

type ColumnReference struct {
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Position Position `json:"-"`
}

type UnaryExpression struct {
	Type     string     `json:"type"`
	Operator string     `json:"operator"`
	Operand  Expression `json:"operand"`
	Position Position   `json:"-"`
}

type BinaryExpression struct {
	Type     string     `json:"type"`
	Operator string     `json:"operator"`
	Left     Expression `json:"left"`
	Right    Expression `json:"right"`
	Position Position   `json:"-"`
}

func (*Literal) expressionNode()          {}
func (*ColumnReference) expressionNode()  {}
func (*UnaryExpression) expressionNode()  {}
func (*BinaryExpression) expressionNode() {}
