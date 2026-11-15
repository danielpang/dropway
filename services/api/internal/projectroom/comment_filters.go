package projectroom

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

// ThreadFilters apply to thread creation time, not reply or resolution time.
// Image comments are point anchors carrying intrinsic image coordinates.
type ThreadFilters struct {
	VersionID     string `json:"version_id,omitempty"`
	CreatedAfter  string `json:"created_after,omitempty"`
	CreatedBefore string `json:"created_before,omitempty"`
	Type          string `json:"type,omitempty"`
}

func (f ThreadFilters) Validate() error {
	if f.VersionID != "" {
		if _, err := uuid.Parse(f.VersionID); err != nil {
			return fmt.Errorf("invalid version_id")
		}
	}
	switch f.Type {
	case "", "text", "component", "image", "point", "page":
	default:
		return fmt.Errorf("invalid comment type")
	}
	var after, before time.Time
	var err error
	if f.CreatedAfter != "" {
		after, err = time.Parse(time.RFC3339Nano, f.CreatedAfter)
		if err != nil {
			return fmt.Errorf("created_after must be RFC3339")
		}
	}
	if f.CreatedBefore != "" {
		before, err = time.Parse(time.RFC3339Nano, f.CreatedBefore)
		if err != nil {
			return fmt.Errorf("created_before must be RFC3339")
		}
	}
	if !after.IsZero() && !before.IsZero() && !after.Before(before) {
		return fmt.Errorf("created_after must precede created_before")
	}
	return nil
}
func (f ThreadFilters) Binding(roomID string) string {
	if f == (ThreadFilters{}) {
		return roomID
	}
	raw, _ := json.Marshal(f)
	return roomID + ":" + string(raw)
}
