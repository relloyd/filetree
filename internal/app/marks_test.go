package app

import (
	"reflect"
	"testing"
)

func TestDedupeAncestors(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "child of marked dir is dropped",
			in:   []string{"/r/dir/file.go", "/r/dir", "/r/other.go"},
			want: []string{"/r/dir", "/r/other.go"},
		},
		{
			name: "deep descendant is dropped",
			in:   []string{"/r/a", "/r/a/b/c/d.txt"},
			want: []string{"/r/a"},
		},
		{
			name: "sibling with shared name prefix is kept",
			in:   []string{"/r/dir", "/r/dir2/file.go"},
			want: []string{"/r/dir", "/r/dir2/file.go"},
		},
		{
			name: "unrelated paths keep their order",
			in:   []string{"/r/z.go", "/r/a.go"},
			want: []string{"/r/z.go", "/r/a.go"},
		},
	}
	for _, c := range cases {
		if got := dedupeAncestors(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
