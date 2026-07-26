package tmux

import "testing"

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
