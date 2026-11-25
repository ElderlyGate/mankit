package serve

import (
	"encoding/json"
	"testing"

	"go.naywee.com/contracts/v2"

	"go.naywee.com/mankit/v5/problem"
)

// The envelope serve says it writes is the one its answers meet, so the constant and the writer cannot drift apart.
func TestTheEnvelopeServeWritesMeetsItsSchema(t *testing.T) {
	a := App{ID: "designer", Version: "5.0.0", Contracts: []Contract{{Version: "v1"}}}
	for _, e := range []Envelope{a.Answer("shop", "v1", "Greeting", map[string]string{"text": "hello"}), a.Failure("shop", "v1", problem.Fail(problem.TypeNotFound, "", "nothing here"))} {
		body, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := contracts.Validate("envelope/"+envelope+"/envelope.schema.json", body); err != nil {
			t.Fatalf("not envelope/%s: %v\n%s", envelope, err, body)
		}
	}
}
