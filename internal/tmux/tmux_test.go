package tmux

import (
	"slices"
	"testing"
)

func TestShouldWrap(t *testing.T) {
	// The one configuration that wraps: auto mode, a real terminal, tmux
	// installed, and no multiplexer already around us.
	ok := Env{Mode: ModeAuto, HasTmux: true, TTY: true}

	cases := []struct {
		name string
		env  Env
		want bool
	}{
		{"outside any multiplexer", ok, true},
		{"already inside tmux", withEnv(ok, func(e *Env) { e.TMUX = "/tmp/tmux-501/default,123,0" }), false},
		{"inside GNU screen", withEnv(ok, func(e *Env) { e.STY = "1234.pts-0.host" }), false},
		{"inside zellij", withEnv(ok, func(e *Env) { e.ZELLIJ = "0" }), false},
		{"tmux not installed", withEnv(ok, func(e *Env) { e.HasTmux = false }), false},
		{"output not a terminal", withEnv(ok, func(e *Env) { e.TTY = false }), false},
		{"mode never", withEnv(ok, func(e *Env) { e.Mode = ModeNever }), false},
		{"mode unset", withEnv(ok, func(e *Env) { e.Mode = "" }), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldWrap(tc.env); got != tc.want {
				t.Errorf("ShouldWrap(%+v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func withEnv(e Env, f func(*Env)) Env {
	f(&e)
	return e
}

func TestWrapArgs(t *testing.T) {
	titles := []string{
		";", "set-option", "set-titles", "on",
		";", "set-option", "set-titles-string", TitleFormat,
	}
	cases := []struct {
		name string
		want []string
	}{
		{"ft/tree/repo-1a2b3c4d", append([]string{"tmux", "new-session", "-s", "ft/tree/repo-1a2b3c4d",
			"-c", "/my repo", "/bin/ft", "/my repo"}, titles...)},
		{"", append([]string{"tmux", "new-session",
			"-c", "/my repo", "/bin/ft", "/my repo"}, titles...)},
	}
	for _, tc := range cases {
		if got := WrapArgs("/bin/ft", "/my repo", tc.name); !slices.Equal(got, tc.want) {
			t.Errorf("WrapArgs(name %q) =\n  %q\nwant\n  %q", tc.name, got, tc.want)
		}
	}
}
