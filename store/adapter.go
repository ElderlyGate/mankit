package store

import (
	"context"

	"github.com/tugbayatilla/mankit/v3/features"
)

// adapter is how a scope keeps its things; files is the one every build has, always on.
type adapter interface {
	get(key Key) (Item, bool, error)
	// look is what is at key now: a file's body, or a folder.
	look(key Key) (body []byte, there, folder bool, err error)
	there(thing Key) bool
	list(prefix Key) ([]Key, error)
	revision() (string, error)
	// settle puts back what this user's change cut short left in the lines a change to thing meets, before the change waits for its turn.
	settle(ctx context.Context, thing Key) error
	// blocked refuses a key a change cut short still holds, or with an empty key, the folder itself.
	blocked(t *turns, key Key) error
	// commit makes ops all or nothing inside the turns held, and reads them back.
	commit(t *turns, ops []op) error
	unfinished(thing Key) ([]Unfinished, error)
	clear(ctx context.Context, thing Key) ([]Unfinished, error)
	close() error
}

// opener is the module an app attaches to an enterprise feature (ADR-E9), such as a database, to keep scopes in something other than files.
type opener interface {
	open(s *Scope) (adapter, error)
}

// pick is the one place a scope's adapter is chosen (G-CODE-7): the module of the first feature in the app's list that is an opener and is on, else files.
// A feature that is on but cannot run refuses the scope; it never falls back to files (ADR-S22).
func pick(list *features.List, s *Scope) (adapter, error) {
	if list != nil {
		for _, p := range list.Parts() {
			o, ok := p.Part.(opener)
			if !ok {
				continue
			}
			on, err := list.On(p.ID)
			if err != nil {
				return nil, err
			}
			if on {
				return o.open(s)
			}
		}
	}
	return openFiles(s)
}
