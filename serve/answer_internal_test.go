package serve

import (
	"encoding/json"
	"testing"

	"github.com/ElderlyGate/contracts"
)

// Every envelope serve says it writes has its branch in MarshalJSON and meets its contracts schema, so envelopes and the writer cannot drift apart.
func TestEveryEnvelopeHasItsWriter(t *testing.T) {
	for _, form := range envelopes {
		a := App{ID: "designer", Version: "5.0.0", Contracts: []Contract{{Version: "v1", Envelope: form}}}
		for _, e := range []Envelope{a.Answer("shop", "v1", "Greeting", map[string]string{"text": "hello"}), a.Failure("shop", "v1", Fail(TypeNotFound, "", "nothing here"))} {
			body, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("envelope %s: %v", form, err)
			}
			if err := contracts.Validate("envelope/"+form+"/envelope.schema.json", body); err != nil {
				t.Fatalf("envelope %s: not envelope/%s: %v\n%s", form, form, err, body)
			}
		}
	}
	if _, err := json.Marshal(Envelope{form: "v9"}); err == nil {
		t.Fatal("an envelope with no writer was written")
	}
}
