package tmux

const (
	// ValueStateUnavailable indicates the value was not set, is absent, or was not returned by tmux.
	ValueStateUnavailable ValueState = iota

	// ValueStatePresent indicates the value was observed and is present.
	// An empty string or zero integer can be ValueStatePresent (distinguishable from unset).
	ValueStatePresent

	// ValueStateUnsupported indicates this option or property is not supported by the answering tmux version.
	ValueStateUnsupported
)

const (
	// ScopeServer applies daemon-wide across all sessions and windows (tmux set-option -s).
	ScopeServer Scope = iota

	// ScopeGlobalSession defines default session options inherited by newly created sessions (tmux set-option -g).
	ScopeGlobalSession

	// ScopeSession applies to a specific session target (tmux set-option -t $session).
	ScopeSession

	// ScopeGlobalWindow defines default window options inherited by newly created windows (tmux set-option -g -w).
	ScopeGlobalWindow

	// ScopeWindow applies to a specific window target (tmux set-option -w -t @window).
	ScopeWindow

	// ScopePane applies to a specific pane target (tmux set-option -p -t %pane).
	ScopePane
)

const (
	// TransportSubprocess executes commands via independent, short-lived tmux CLI subprocess invocations.
	TransportSubprocess Transport = iota

	// TransportControl executes commands over a persistent, bi-directional tmux -C control mode connection.
	TransportControl
)

// ValueState distinguishes unsupported features, unavailable/unset values,
// and explicitly present values (including present empty strings).
type (
	ValueState uint8
	Scope      uint8
	Transport  uint8
)

// Value represents an optional value of type T.
// The zero value of Value is [ValueStateUnavailable].
type Value[T any] struct {
	value T
	state ValueState
}

// OptionValue captures an option value within tmux's inheritance hierarchy.
type OptionValue[T any] struct {
	// Local is the value set at this specific target, or [ValueStateUnavailable] if inherited.
	Local Value[T]

	// Effective is the active value in effect at this target.
	Effective Value[T]

	// Origin is the exact [Scope] where the effective value was defined, if provable.
	Origin Value[Scope]
}

// PresentValue returns a [Value] in the [ValueStatePresent] state wrapping v.
func PresentValue[T any](v T) Value[T] { return Value[T]{value: v, state: ValueStatePresent} }

// UnavailableValue returns a [Value] in the [ValueStateUnavailable] state.
func UnavailableValue[T any]() Value[T] {
	var zero T
	return Value[T]{value: zero, state: ValueStateUnavailable}
}

// UnsupportedValue returns a [Value] in the [ValueStateUnsupported] state.
func UnsupportedValue[T any]() Value[T] {
	var zero T
	return Value[T]{value: zero, state: ValueStateUnsupported}
}

// Get returns the underlying value and true if the state is [ValueStatePresent].
// If the value is [ValueStateUnavailable] or [ValueStateUnsupported], it returns the zero value and false.
func (v Value[T]) Get() (T, bool) { return v.value, v.state == ValueStatePresent }

// State reports whether the value is [ValueStatePresent], [ValueStateUnavailable], or [ValueStateUnsupported].
func (v Value[T]) State() ValueState { return v.state }
