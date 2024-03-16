package controller

type operation int

const (
	AddOp operation = iota
	UpdateOp
	DeleteOp
)

type KeyOp struct {
	Key       string
	Attempts  int
	Obj       interface{}
	Operation operation
}

func NewKeyOp(key string, obj interface{}, op operation) KeyOp {
	return KeyOp{
		Key:       key,
		Obj:       obj,
		Operation: op,
	}
}
