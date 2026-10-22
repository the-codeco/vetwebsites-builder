package blocks

func init() {
	Register(&Type{Name: ReusableType, Label: "Shared block", Internal: true, Fields: []Field{
		{Key: "ref", Label: "Shared block", Kind: KindNumber, Default: 0.0, Min: 0, Max: 1e15},
	}})
}
