package domain

import "testing"

// AF-04: lost is final and only follows a handover; exception facts map
// to their own outbox types; a redelivery follows a failed attempt only.
func TestLostAndExceptionFacts(t *testing.T) {
	if !CanTransition(StatusShipped, StatusLost) || !CanTransition(StatusInterceptionRequested, StatusLost) {
		t.Fatal("a package with the carrier can be lost")
	}
	if CanTransition(StatusReadyToShip, StatusLost) || CanTransition(StatusLost, StatusShipped) || CanTransition(StatusReturned, StatusShipped) {
		t.Fatal("lost needs a handover and no final attempt is reset")
	}
	if !StatusLost.Final() || StatusLost.Active() || !StatusPending.Active() {
		t.Fatal("lost is final")
	}
	if _, ok := OrderEventFor(StatusLost); ok {
		t.Fatal("old Order consumers never see the new status")
	}
	for _, kind := range []ExceptionType{ExceptionAttemptsExhausted, ExceptionReturned, ExceptionLost} {
		if back, ok := ExceptionTypeOf(kind.OutboxType()); !ok || back != kind {
			t.Fatalf("%s round trip", kind)
		}
	}
	if _, ok := ExceptionTypeOf(OrderEventReturned); ok {
		t.Fatal("the status fact is not an exception fact")
	}
	if !RedeliverableFrom(StatusReturned) || !RedeliverableFrom(StatusLost) || RedeliverableFrom(StatusDelivered) || RedeliverableFrom(StatusShipped) {
		t.Fatal("a redelivery follows a returned or lost attempt")
	}
}
