package app

import "testing"

func TestParseFindQuery(t *testing.T) {
	tests := []struct {
		in      string
		include string
		exclude []string
	}{
		{"", "", nil},
		{"spanner", "spanner", nil},
		{"foo bar", "foo bar", nil},
		{"!nonprod", "", []string{"nonprod"}},
		{"spanner !nonprod", "spanner", []string{"nonprod"}},
		{"!nonprod !sandbox", "", []string{"nonprod", "sandbox"}},
		{"!NonProd", "", []string{"nonprod"}},
		{"!", "", nil},
		{"foo!bar", "foo!bar", nil},
		{"  !nonprod  spanner ", "spanner", []string{"nonprod"}},
		{"prod !nonprod", "prod", []string{"nonprod"}},
	}
	for _, tt := range tests {
		got := parseFindQuery(tt.in)
		if got.include != tt.include {
			t.Errorf("parseFindQuery(%q).include = %q, want %q", tt.in, got.include, tt.include)
		}
		if len(got.exclude) != len(tt.exclude) {
			t.Errorf("parseFindQuery(%q).exclude = %v, want %v", tt.in, got.exclude, tt.exclude)
			continue
		}
		for i, ex := range tt.exclude {
			if got.exclude[i] != ex {
				t.Errorf("parseFindQuery(%q).exclude[%d] = %q, want %q", tt.in, i, got.exclude[i], ex)
			}
		}
		if tt.in == "" && !got.blank() {
			t.Errorf("empty query should be blank")
		}
	}
}

func TestFindQueryExcluded(t *testing.T) {
	path := "terraform/providers/nonprod/spanner-instance.tf"
	if parseFindQuery("!nonprod").excluded(path) != true {
		t.Errorf("!nonprod should exclude %q", path)
	}
	if parseFindQuery("!NonProd").excluded(path) != true {
		t.Errorf("!NonProd should exclude %q (case-insensitive)", path)
	}
	if parseFindQuery("!sandbox").excluded(path) {
		t.Errorf("!sandbox should keep %q", path)
	}
	if parseFindQuery("!nonprod !sandbox").excluded("infra/sandbox/foo.tf") != true {
		t.Error("any exclude hitting should drop the path")
	}
	if parseFindQuery("spanner").excluded(path) {
		t.Error("a query with no excludes should never exclude")
	}
	if parseFindQuery("").excluded(path) {
		t.Error("a blank query should never exclude")
	}
	if parseFindQuery("!").excluded(path) {
		t.Error("an empty ! token should be ignored")
	}
}
