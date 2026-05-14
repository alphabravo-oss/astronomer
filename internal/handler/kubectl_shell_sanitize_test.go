package handler

// Unit tests for sanitizeRecordedLine — the input-recorder filter
// that strips terminal-protocol noise (CSI escapes, C0 controls)
// from a stdin line before it lands in kubectl_session_commands.
//
// Reproduces the user-reported regression where typing `ls` recorded
// as `[2;5Rls` because xterm.js's reply to the shell's DSR cursor-
// position-report query was being concatenated with the real
// keystrokes in the input buffer.

import "testing"

func TestSanitizeRecordedLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// The exact regression: cursor-position-report (`\x1b[2;5R`)
		// followed by the real command.
		{"cursor_position_then_ls", "\x1b[2;5Rls", "ls"},
		{"trailing_cursor_position", "ls\x1b[2;5R", "ls"},
		{"interleaved_cursor_position", "l\x1b[2;5Rs", "ls"},

		// Arrow keys + editing keystrokes echoed back as CSI.
		{"arrow_up", "\x1b[Akubectl get pods", "kubectl get pods"},
		{"ctrl_right", "kubectl\x1b[1;5C get", "kubectl get"},

		// Bracketed-paste markers — xterm.js sometimes echoes them
		// on paste. Drop the markers, keep the content.
		{"bracketed_paste", "\x1b[200~echo hi\x1b[201~", "echo hi"},

		// C0 controls other than tab → dropped.
		{"backspace", "kubectl ge\x7ft pods", "kubectl get pods"},
		{"null_byte", "foo\x00bar", "foobar"},
		{"bell", "ls\x07", "ls"},

		// Tab is preserved — operators may use it as completion or
		// in pasted text.
		{"tab_preserved", "ls\tfoo", "ls\tfoo"},

		// Already-clean input untouched.
		{"plain", "kubectl get pods -n kube-system", "kubectl get pods -n kube-system"},
		{"empty", "", ""},

		// Trailing whitespace trimmed (single tab/space at end is
		// almost always an editing artefact, not intent).
		{"trailing_space", "ls  ", "ls"},

		// Unterminated CSI (operator hit ESC mid-edit, then sent the
		// line) — drop the dangling escape, keep the rest.
		{"unterminated_csi", "ls\x1b[", "ls"},

		// Lone ESC — dropped.
		{"lone_esc", "\x1bls", "ls"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeRecordedLine([]byte(c.in))
			if got != c.want {
				t.Errorf("sanitizeRecordedLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
