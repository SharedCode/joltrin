package nats

import (
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
)

// Subscribe subscribes to subject on nc and calls handler with each
// BarrierDecision received. It is a thin convenience wrapper around
// nc.Subscribe for the common case of consuming VerifyBridge's own output;
// nothing in this package requires a caller to use it, a NATS subscriber
// in any other language or client can consume SubjectFor's subject
// directly since BarrierDecision is plain JSON.
//
// A message that fails to unmarshal as a BarrierDecision is dropped rather
// than passed to handler or returned as an error, since one malformed
// message on a shared subject should not stop delivery of the rest; wrap
// nc.Subscribe directly instead if that behavior does not fit a caller's
// needs.
func Subscribe(nc *nats.Conn, subject string, handler func(BarrierDecision)) (*nats.Subscription, error) {
	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		var ev BarrierDecision
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			return
		}
		handler(ev)
	})
	if err != nil {
		return nil, fmt.Errorf("nats: subscribe to %q: %w", subject, err)
	}
	return sub, nil
}
