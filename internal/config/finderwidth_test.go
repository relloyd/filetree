package config

import "testing"

func TestFinderWidthCells(t *testing.T) {
	for _, tc := range []struct {
		name   string
		spec   string
		window int
		want   int
	}{
		{"a percentage of the window", "60%", 200, 120},
		{"rounds down", "33%", 100, 33},
		{"a plain column count", "90", 200, 90},
		{"off", "off", 200, 0},
		{"OFF, whatever the case", "OFF", 200, 0},
		{"empty is off", "", 200, 0},
		{"whitespace is trimmed", "  60%  ", 200, 120},
		{"clamped to the window", "300", 100, 100},
		{"over 100% is clamped, not refused", "150%", 100, 100},
		{"below the floor is off, not a crushed pane", "5", 200, 0},
		{"a percentage below the floor is off too", "5%", 200, 0},
		{"nonsense is off rather than fatal", "wide", 200, 0},
		{"an unknown window is off", "60%", 0, 0},
		{"a negative count is off", "-40", 200, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FinderWidthCells(tc.spec, tc.window); got != tc.want {
				t.Errorf("FinderWidthCells(%q, %d) = %d, want %d", tc.spec, tc.window, got, tc.want)
			}
		})
	}
}

// The validator is deliberately looser than the resolver: a setting that
// resolves to "leave the pane alone" is still a setting worth accepting, since
// the window it is measured against is not known until ft is running.
func TestValidFinderWidth(t *testing.T) {
	for _, s := range []string{"60%", "off", "OFF", "", "  ", "90", "150%", "5"} {
		if !ValidFinderWidth(s) {
			t.Errorf("ValidFinderWidth(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"wide", "-40", "60 %", "%", "60%%", "sixty"} {
		if ValidFinderWidth(s) {
			t.Errorf("ValidFinderWidth(%q) = true, want false", s)
		}
	}
}

// The default has to be one the validator accepts, or a first run writes a
// config that ft then refuses to load.
func TestDefaultFinderWidthIsValid(t *testing.T) {
	if !ValidFinderWidth(DefaultFinderWidth) {
		t.Fatalf("DefaultFinderWidth %q is not valid", DefaultFinderWidth)
	}
	if Default().General.FinderWidth != DefaultFinderWidth {
		t.Error("Default() does not use DefaultFinderWidth")
	}
}
