package serve_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tugbayatilla/mankit/v3/events"
	"github.com/tugbayatilla/mankit/v3/features"
	"github.com/tugbayatilla/mankit/v3/queue"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// A contract still on envelope/v1 keeps the bytes mankit v2.0.0 wrote (CT-RULE-1): testdata/v1-golden holds what v2.0.0 wrote for each refusal.
func TestAContractOnEnvelopeV1KeepsItsBytes(t *testing.T) {
	app := serve.App{ID: "designer", Version: "5.0.0", Contracts: []string{"v1"}}
	two := serve.App{ID: "designer", Version: "5.0.0", Contracts: []string{"v1", "v2"}}
	_, unsupported := two.Contract("v9")
	for golden, e := range map[string]serve.Envelope{
		"host_refused":              app.Failure("shop", "v1", serve.Fail(serve.TypeRefused, serve.ReasonHost, "designer answers on this machine's own address only; serve --host <name> names another")),
		"origin_refused":            app.Failure("shop", "v1", serve.Fail(serve.TypeRefused, serve.ReasonOrigin, "designer takes an API request only from its own page")),
		"token_refused":             app.Failure("shop", "v1", serve.Fail(serve.TypeRefused, serve.ReasonToken, "designer takes an API request only with its token, as Authorization: Bearer <token> or in the cookie the page's link left; `designer servers link` prints the link")),
		"not_json":                  app.Failure("shop", "v1", serve.Fail(serve.TypeRefused, serve.ReasonNotJSON, "designer takes a request body as JSON only (Content-Type: application/json)")),
		"unconfirmed":               app.Failure("shop", "v1", serve.Fail(serve.TypeRefused, serve.ReasonUnconfirmed, "nothing was changed: block asks first; send it again with confirm=yes once the user said yes")),
		"not_found":                 app.Failure("shop", "v1", serve.Fail(serve.TypeNotFound, "", "designer has no GET /api/nothing")),
		"config_unreadable":         app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, serve.ReasonConfigUnreadable, "the feature tracing cannot be read: bad")),
		"failed":                    app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, "", "something broke")),
		"core_needs_values":         app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, serve.ReasonCoreNeedsValues, "designer cannot start: it needs values that are not set: ED_X")),
		"feature_needs_values":      app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, features.ReasonNeedsValues, "the feature tracing needs values that are not set: ED_X")),
		"too_many_streams":          app.Failure("shop", "v1", serve.Fail(serve.TypeRefused, events.ReasonTooManyStreams, "8 streams are open, the most this server keeps; try again later")),
		"queue_timeout":             app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, queue.ReasonTimeout, "the change waited 30s for its turn in /q and did not run")),
		"not_read_back":             app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, queue.ReasonNotReadBack, "c was written but could not be read back: x")),
		"ticket_lost":               app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, queue.ReasonLost, "the ticket t was taken for stale and moved to /q/bad; the change did not run")),
		"queue_unusable":            app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, queue.ReasonUnusable, "the change queue cannot use /q as its folder: x")),
		"queue_backend_unsupported": app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, queue.ReasonBackendUnsupported, "the change queue's marker /q/backend is not JSON: x; no change is made here until it names \"files\"")),
		"change_too_long":           app.Failure("shop", "v1", serve.Fail(serve.TypeFailed, queue.ReasonTooLong, "c held the turn in /q past the limit of 1m0s and stopped: x")),
		"unsupported_contract":      two.Failure("shop", "v2", unsupported),
		"a-plain-error":             app.Failure("shop", "v1", errors.New("plain & <odd>")),
		"an-answer":                 app.Answer("shop", "v1", "Greeting", map[string]any{"text": "hello <you> & me"}),
	} {
		want, err := os.ReadFile(filepath.Join("testdata", "v1-golden", golden+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if err := serve.Encode(&got, e); err != nil {
			t.Fatal(err)
		}
		if got.String() != string(want) {
			t.Errorf("%s: v1 bytes changed\ngot  %s\nwant %s", golden, got.String(), want)
		}
	}
}
