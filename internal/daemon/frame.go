package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// decodeFrame accepts both existing single-event lines and event batches.
func decodeFrame(b []byte) ([]event.Event, error) {
	var header struct {
		Events json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(b, &header); err != nil {
		return nil, err
	}
	if header.Events != nil {
		var events []event.Event
		if err := json.Unmarshal(header.Events, &events); err != nil {
			return nil, fmt.Errorf("decoding event batch: %w", err)
		}
		return events, nil
	}
	var ev event.Event
	if err := json.Unmarshal(b, &ev); err != nil {
		return nil, err
	}
	return []event.Event{ev}, nil
}
