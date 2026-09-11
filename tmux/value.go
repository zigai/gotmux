package tmux

const (
	// Unavailable indicates the value was not set, is absent, or was not returned by tmux.
	Unavailable ValueState = iota

	// Present indicates the value was observed and is present.
	// An empty string or zero integer can be Present (distinguishable from unset).
	Present

	// Unsupported indicates this option or property is not supported by the answering tmux version.
	Unsupported
)

const (
	// ServerScope applies daemon-wide across all sessions and windows (tmux set-option -s).
	ServerScope Scope = iota

	// GlobalSessionScope defines default session options inherited by newly created sessions (tmux set-option -g).
	GlobalSessionScope

	// SessionScope applies to a specific session target (tmux set-option -t $session).
	SessionScope

	// GlobalWindowScope defines default window options inherited by newly created windows (tmux set-option -g -w).
	GlobalWindowScope

	// WindowScope applies to a specific window target (tmux set-option -w -t @window).
	WindowScope

	// PaneScope applies to a specific pane target (tmux set-option -p -t %pane).
	PaneScope
)

const (
	// Subprocess executes commands via independent, short-lived tmux CLI subprocess invocations.
	Subprocess Transport = iota

	// Control executes commands over a persistent, bi-directional tmux -C control mode connection.
	Control
)

// ValueState distinguishes unsupported features, unavailable/unset values,
// and explicitly present values (including present empty strings).
type (
	ValueState uint8
	Scope      uint8
	Transport  uint8
)

// Value represents an optional value of type T.
// The zero value of Value is Unavailable.
type Value[T any] struct {
	value T
	state ValueState
}

// OptionValue captures an option value within tmux's inheritance hierarchy.
type OptionValue[T any] struct {
	// Local is the value set at this specific target, or Unavailable if inherited.
	Local Value[T]

	// Effective is the active value in effect at this target.
	Effective Value[T]

	// Origin is the exact [Scope] where the effective value was defined, if provable.
	Origin Value[Scope]
}

// PresentValue returns a [Value] in the [Present] state wrapping v.
func PresentValue[T any](v T) Value[T] { return Value[T]{value: v, state: Present} }

// UnavailableValue returns a [Value] in the [Unavailable] state.
func UnavailableValue[T any]() Value[T] {
	var zero T
	return Value[T]{value: zero, state: Unavailable}
}

// UnsupportedValue returns a [Value] in the [Unsupported] state.
func UnsupportedValue[T any]() Value[T] {
	var zero T
	return Value[T]{value: zero, state: Unsupported}
}

// Get returns the underlying value and true if the state is [Present].
// If the value is Unavailable or Unsupported, it returns the zero value and false.
func (v Value[T]) Get() (T, bool) { return v.value, v.state == Present }

// State reports whether the value is [Present], [Unavailable], or [Unsupported].
func (v Value[T]) State() ValueState { return v.state }
