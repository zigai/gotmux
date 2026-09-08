package tmux

// ValueState distinguishes unsupported, unavailable, and present (even empty).
type ValueState uint8

const (
	Unavailable ValueState = iota
	Present
	Unsupported
)

// Value owns an optional value. The zero value is unavailable.
type Value[T any] struct {
	value T
	state ValueState
}

func PresentValue[T any](v T) Value[T]  { return Value[T]{value: v, state: Present} }
func UnavailableValue[T any]() Value[T] { return Value[T]{} }
func UnsupportedValue[T any]() Value[T] { return Value[T]{state: Unsupported} }
func (v Value[T]) Get() (T, bool)       { return v.value, v.state == Present }
func (v Value[T]) State() ValueState    { return v.state }

type Scope uint8

const (
	ServerScope Scope = iota
	GlobalSessionScope
	SessionScope
	GlobalWindowScope
	WindowScope
	PaneScope
)

type OptionValue[T any] struct {
	Local     Value[T]
	Effective Value[T]
	Origin    Value[Scope]
}
type Transport uint8

const (
	Subprocess Transport = iota
	Control
)
