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

func TestResolve(t *testing.T) {
	tests := []struct {
		name       string
		typ        Type
		product    map[Type]Style
		global     map[Type]Style
		wantStyle  Style
		wantSource Source
	}{
		{"product wins", Move, map[Type]Style{Move: Never}, map[Type]Style{Move: Name}, Never, FromProduct},
		{"global when the product has none", Move, map[Type]Style{Delete: Never}, map[Type]Style{Move: Name}, Name, FromGlobal},
		{"default for delete", Delete, nil, nil, Name, FromDefault},
		{"default for the rest", Move, nil, map[Type]Style{Delete: Never}, YesNo, FromDefault},
		{"unknown product style is not set", Move, map[Type]Style{Move: "always"}, map[Type]Style{Move: Never}, Never, FromGlobal},
		{"unknown global style is not set", Move, nil, map[Type]Style{Move: ""}, YesNo, FromDefault},
	}
	for _, tt := range tests {
		if s, src := Resolve(tt.typ, tt.product, tt.global); s != tt.wantStyle || src != tt.wantSource {
			t.Errorf("%s: Resolve = %s, %s; want %s, %s", tt.name, s, src, tt.wantStyle, tt.wantSource)
		}
	}
}
