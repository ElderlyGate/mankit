package ask

import "testing"

func TestDefault(t *testing.T) {
	for _, typ := range Types() {
		want := YesNo
		if typ == Delete {
			want = Name
		}
		if got := Default(typ); got != want {
			t.Errorf("Default(%s) = %s, want %s", typ, got, want)
		}
	}
}

func TestValidType(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"create", true}, {"edit", true}, {"move", true}, {"focus", true}, {"archive", true},
		{"close", true}, {"delete", true}, {"settings", true}, {"connect", true}, {"run", true},
		{"", false}, {"Delete", false}, {"remove", false},
	}
	for _, tt := range tests {
		if got := ValidType(tt.in); got != tt.want {
			t.Errorf("ValidType(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseStyle(t *testing.T) {
	tests := []struct {
		in      string
		want    Style
		wantErr string
	}{
		{"name", Name, ""},
		{"yes-no", YesNo, ""},
		{"never", Never, ""},
		{"", "", `unknown style "": use name, yes-no or never`},
		{"always", "", `unknown style "always": use name, yes-no or never`},
	}
	for _, tt := range tests {
		got, err := ParseStyle(tt.in)
		if got != tt.want || (err == nil) != (tt.wantErr == "") || (err != nil && err.Error() != tt.wantErr) {
			t.Errorf("ParseStyle(%q) = %q, %v; want %q, %q", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}
