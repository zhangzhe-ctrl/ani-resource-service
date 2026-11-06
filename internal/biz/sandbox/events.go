package sandbox

import (
	"context"
	"time"
)

// Event is a safe projection of an event for a recorded Sandbox Pod.
type Event struct {
	ID, Type, Reason, Message string
	OccurredAt                time.Time
}

type EventReader interface {
	ReadEvents(context.Context, Sandbox, []PodHistory) ([]Event, error)
}
