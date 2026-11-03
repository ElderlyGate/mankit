package ask

import "testing"

func TestDefault(t *testing.T) {
	for _, typ := range Types() {
		if got := Default(Action{Type: typ}); got != YesNo {
			t.Errorf("Default(%s) = %s, want %s", typ, got, YesNo)
		}
		if got := Default(Action{Type: typ, System: true}); got != Name {
			t.Errorf("Default(%s, system) = %s, want %s", typ, got, Name)
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
		action     Action
		product    map[Type]Style
		wantStyle  Style
		wantSource Source
	}{
		{"product value", Action{Type: Move}, map[Type]Style{Move: Never}, Never, FromProduct},
		{"default when the product has none", Action{Type: Move}, map[Type]Style{Delete: Never}, YesNo, FromDefault},
		{"default for delete", Action{Type: Delete}, nil, YesNo, FromDefault},
		{"default for a system change", Action{Type: Delete, System: true}, nil, Name, FromDefault},
		{"product value for a system change", Action{Type: Delete, System: true}, map[Type]Style{Delete: YesNo}, YesNo, FromProduct},
		{"a remembered never does not silence a system change", Action{Type: Delete, System: true}, map[Type]Style{Delete: Never}, Name, FromDefault},
		{"a never still silences the type's other changes", Action{Type: Delete}, map[Type]Style{Delete: Never}, Never, FromProduct},
		{"unknown product style is not set", Action{Type: Move}, map[Type]Style{Move: "always"}, YesNo, FromDefault},
	}
	for _, tt := range tests {
		if s, src := Resolve(tt.action, tt.product); s != tt.wantStyle || src != tt.wantSource {
			t.Errorf("%s: Resolve = %s, %s; want %s, %s", tt.name, s, src, tt.wantStyle, tt.wantSource)
		}
	}
}
