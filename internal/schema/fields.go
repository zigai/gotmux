// Package schema is the checked-in metadata dialect for stock tmux 3.6+.
// Fields in these lists are an explicit supported schema, not all tmux formats.
package schema

var (
	Identity = []string{"pid", "start_time", "socket_path", "version"}
	Session  = []string{"session_id", "session_name", "session_path", "session_created", "session_activity", "session_attached", "session_windows", "session_group", "session_grouped"}
	Window   = []string{"window_id", "window_name", "window_width", "window_height", "window_panes", "window_layout", "window_zoomed_flag", "session_id", "window_index", "window_active", "window_flags"}
	Pane     = []string{"pane_id", "window_id", "pane_index", "pane_title", "pane_current_path", "pane_current_command", "pane_pid", "pane_tty", "pane_width", "pane_height", "pane_left", "pane_top", "pane_active", "history_size", "alternate_on", "pane_dead", "pane_dead_status", "cursor_x", "cursor_y", "pane_in_mode", "pane_mode", "selection_present", "selection_start_x", "selection_start_y", "selection_end_x", "selection_end_y", "rectangle_toggle", "scroll_position", "session_id", "session_name", "window_name", "window_index"}
	Client   = []string{"client_name", "client_tty", "client_pid", "client_created", "client_activity", "client_width", "client_height", "session_id", "client_control_mode", "client_readonly", "client_flags"}
)

func WithIdentity(fields []string) []string {
	out := append([]string{}, Identity...)
	return append(out, fields...)
}
