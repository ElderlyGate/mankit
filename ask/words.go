package ask

import (
	"fmt"
	"slices"
)

// Type is the kind of change an action makes; every product maps each change to one.
type Type string

// The action types, one word each, the same in every product.
const (
	Create   Type = "create"
	Edit     Type = "edit"
	Move     Type = "move"
	Focus    Type = "focus"
	Archive  Type = "archive"
	Close    Type = "close"
	Delete   Type = "delete"
	Settings Type = "settings"
	Connect  Type = "connect"
	Run      Type = "run"
)

// Style is how the user wants a type of change to ask.
type Style string

// The styles: type the name, answer y/N, or never ask.
const (
	Name  Style = "name"
	YesNo Style = "yes-no"
	Never Style = "never"
)

var (
	types  = []Type{Create, Edit, Move, Focus, Archive, Close, Delete, Settings, Connect, Run}
	styles = []Style{Name, YesNo, Never}
)

// Types lists every action type, in the order a settings page shows them.
func Types() []Type { return slices.Clone(types) }

// ValidType reports whether s is one of the action types.
func ValidType(s string) bool { return slices.Contains(types, Type(s)) }

// ParseStyle returns the style s names, or an error naming the allowed ones.
func ParseStyle(s string) (Style, error) {
	if slices.Contains(styles, Style(s)) {
		return Style(s), nil
	}
	return "", fmt.Errorf("unknown style %q: use name, yes-no or never", s)
}

// Default is the style of a type the user has not chosen: name for delete, yes-no for the rest.
func Default(t Type) Style {
	if t == Delete {
		return Name
	}
	return YesNo
}

// Source says where the style in use comes from.
type Source string

// The sources: the product's own config, or the default.
const (
	FromProduct Source = "product"
	FromDefault Source = "default"
)

// Resolve is the style in use for t and where it comes from: the product's value, else Default(t); an unknown style counts as not set.
func Resolve(t Type, product map[Type]Style) (Style, Source) {
	if s, ok := product[t]; ok && slices.Contains(styles, s) {
		return s, FromProduct
	}
	return Default(t), FromDefault
}
